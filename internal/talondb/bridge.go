package talondb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/opentalon/tln-language/internal/factstore"
	pb "github.com/opentalon/talon-db/proto/talondbpb"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Events returns a FactStore-style event emitter that fires every
// time the connected talondb-server commits a mutation. The emitter
// is populated by a background goroutine started by RunEventBridge;
// callers that want events must invoke RunEventBridge with a long-
// lived context before subscribing.
func (a *Adapter) Events() *factstore.EventEmitter {
	a.events.once.Do(func() {
		a.events.emitter = &factstore.EventEmitter{}
	})
	return a.events.emitter
}

// adapterEvents is the lazy-init wrapper around the EventEmitter
// adapter exposes via Events().
type adapterEvents struct {
	once    sync.Once
	emitter *factstore.EventEmitter
}

// RunEventBridge opens a Subscribe stream on the connected client and
// translates each talondb MutationEvent into one or more
// factstore.Events that get dispatched through Adapter.Events().
//
// The function blocks until ctx is cancelled or the connection
// becomes permanently unrecoverable. Stream errors trigger an
// exponential-backoff reconnect (capped at maxReconnectDelay).
// Returns ctx.Err() on clean shutdown.
//
// Typical use:
//
//	adapter := talondb.New(client)
//	go adapter.RunEventBridge(ctx)
//	dispatcher.Subscribe(adapter.Events())
//
// Filters narrow the stream server-side. Empty entityID matches
// every tenant; empty prefix matches every doc.
func (a *Adapter) RunEventBridge(ctx context.Context, entityID, docIDPrefix string) error {
	emitter := a.Events()

	const (
		initialReconnectDelay = 100 * time.Millisecond
		maxReconnectDelay     = 30 * time.Second
	)
	delay := initialReconnectDelay

	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		stream, err := a.client.svc.Subscribe(ctx, &pb.SubscribeRequest{
			EntityId:    entityID,
			DocIdPrefix: docIDPrefix,
		})
		if err != nil {
			if !sleepOrCancel(ctx, delay) {
				return ctx.Err()
			}
			delay = nextDelay(delay, maxReconnectDelay)
			continue
		}
		// Successful connection — reset backoff for the next failure.
		delay = initialReconnectDelay

		streamErr := pumpStream(ctx, stream, emitter)
		if streamErr == nil || errors.Is(streamErr, io.EOF) {
			// Server closed the stream cleanly; reconnect.
			continue
		}
		if errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded) {
			return streamErr
		}
		// Transient gRPC error (Unavailable, ResourceExhausted from
		// subscribe-buffer overflow on the server, etc.) — back off and
		// reconnect. Subscribers will see a gap in events for the
		// duration of the disconnect.
		if !sleepOrCancel(ctx, delay) {
			return ctx.Err()
		}
		delay = nextDelay(delay, maxReconnectDelay)
	}
}

// pumpStream reads events until the stream ends and emits them as
// factstore events.
func pumpStream(ctx context.Context, stream pb.TalonDBService_SubscribeClient, emitter *factstore.EventEmitter) error {
	for {
		ev, err := stream.Recv()
		if err != nil {
			return err
		}
		for _, fev := range translateMutationEvent(ev) {
			emitter.Emit(ctx, fev)
		}
	}
}

// translateMutationEvent converts one doc-level MutationEvent into
// zero or more attribute-level factstore.Events. The mapping mirrors
// the EAV-to-document translation in Adapter.Assert:
//
//   - Assert: every attribute in NewDoc becomes a factstore.EventAssert.
//   - Change: per-attribute delta — new attributes → Assert; removed
//     attributes → Retract; differing values → Change with Prev set.
//   - Retract: every attribute in OldDoc becomes a factstore.EventRetract.
//
// Attributes that exist on both sides with equal values produce no
// event (idempotent — matches MemoryStore semantics).
func translateMutationEvent(ev *pb.MutationEvent) []factstore.Event {
	recordID := ev.GetDocId()
	switch ev.GetKind() {
	case pb.MutationEventKind_MUTATION_EVENT_KIND_ASSERT:
		newAttrs, ok := decodeDocJSON(ev.GetNewDoc())
		if !ok {
			return nil
		}
		out := make([]factstore.Event, 0, len(newAttrs))
		for attr, val := range newAttrs {
			out = append(out, factstore.Event{
				Kind: factstore.EventAssert,
				Fact: factstore.Fact{
					Entity:    ev.GetEntityId(),
					RecordID:  recordID,
					Attribute: attr,
					Value:     val,
				},
			})
		}
		return out

	case pb.MutationEventKind_MUTATION_EVENT_KIND_CHANGE:
		newAttrs, _ := decodeDocJSON(ev.GetNewDoc())
		oldAttrs, _ := decodeDocJSON(ev.GetOldDoc())
		out := make([]factstore.Event, 0, len(newAttrs))
		for attr, newVal := range newAttrs {
			if oldVal, had := oldAttrs[attr]; had {
				if jsonEqual(oldVal, newVal) {
					continue
				}
				out = append(out, factstore.Event{
					Kind: factstore.EventChange,
					Fact: factstore.Fact{Entity: ev.GetEntityId(), RecordID: recordID, Attribute: attr, Value: newVal},
					Prev: factstore.Fact{Entity: ev.GetEntityId(), RecordID: recordID, Attribute: attr, Value: oldVal},
				})
			} else {
				out = append(out, factstore.Event{
					Kind: factstore.EventAssert,
					Fact: factstore.Fact{Entity: ev.GetEntityId(), RecordID: recordID, Attribute: attr, Value: newVal},
				})
			}
		}
		for attr, oldVal := range oldAttrs {
			if _, stillThere := newAttrs[attr]; stillThere {
				continue
			}
			out = append(out, factstore.Event{
				Kind: factstore.EventRetract,
				Fact: factstore.Fact{Entity: ev.GetEntityId(), RecordID: recordID, Attribute: attr, Value: oldVal},
			})
		}
		return out

	case pb.MutationEventKind_MUTATION_EVENT_KIND_RETRACT:
		oldAttrs, ok := decodeDocJSON(ev.GetOldDoc())
		if !ok {
			return nil
		}
		out := make([]factstore.Event, 0, len(oldAttrs))
		for attr, val := range oldAttrs {
			out = append(out, factstore.Event{
				Kind: factstore.EventRetract,
				Fact: factstore.Fact{Entity: ev.GetEntityId(), RecordID: recordID, Attribute: attr, Value: val},
			})
		}
		return out
	}
	return nil
}

// decodeDocJSON parses the doc bytes as a JSON object. Non-JSON or
// non-object content yields (nil, false) — the bridge treats such
// documents as opaque blobs and emits no fact-level events for them
// (matches the adapter's write side, which doesn't decompose opaque
// bytes either).
func decodeDocJSON(raw []byte) (map[string]any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false
	}
	return m, true
}

// jsonEqual compares two JSON-decoded values for structural equality.
// Numbers from encoding/json arrive as float64 already, so direct ==
// works for the common scalar cases; for slices and maps we round-trip
// through json.Marshal which is good-enough at the scale we expect
// (single-doc deltas).
func jsonEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if as, aok := a.(string); aok {
		if bs, bok := b.(string); bok {
			return as == bs
		}
	}
	if af, aok := a.(float64); aok {
		if bf, bok := b.(float64); bok {
			return af == bf
		}
	}
	if ab, aok := a.(bool); aok {
		if bb, bok := b.(bool); bok {
			return ab == bb
		}
	}
	aj, errA := json.Marshal(a)
	bj, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(aj) == string(bj)
}

func sleepOrCancel(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func nextDelay(current, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		return max
	}
	return next
}

// isStreamTerminator is a small helper for tests that want to make a
// stream RPC error surface to the bridge's reconnect path.
//
//nolint:unused // referenced indirectly via translateMutationEvent edge cases
func isStreamTerminator(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.Canceled, codes.DeadlineExceeded, codes.Unavailable, codes.ResourceExhausted:
		return true
	}
	return false
}

// formatStreamErr makes test-side error messages a bit friendlier.
//
//nolint:unused
func formatStreamErr(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("stream terminated: %v", err)
}

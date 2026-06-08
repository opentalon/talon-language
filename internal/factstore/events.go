package factstore

import (
	"context"
	"sync"
)

// EventKind identifies why an event was emitted. Reactive rules subscribe to
// a kind + matching predicate (see internal/reactive/dispatcher.go).
type EventKind int

const (
	// EventAssert fires after a new fact is added to the store.
	EventAssert EventKind = iota
	// EventRetract fires before a fact is removed.
	EventRetract
	// EventChange fires when an existing attribute's value changes.
	// Both Old and New are populated on the Fact pair.
	EventChange
)

func (k EventKind) String() string {
	switch k {
	case EventAssert:
		return "assert"
	case EventRetract:
		return "retract"
	case EventChange:
		return "change"
	}
	return "unknown"
}

// Event is a single mutation observed by the FactStore. The fields populated
// depend on Kind:
//
//	assert  : Fact = new fact, Prev zero
//	retract : Fact = fact being removed, Prev zero
//	change  : Fact = new value, Prev = previous value (same Attribute)
//
// Subscribers receive a value, not a pointer — the FactStore is free to
// reuse internal buffers after dispatch.
type Event struct {
	Kind EventKind
	Fact Fact
	Prev Fact
}

// Subscriber receives events. Returning quickly is encouraged — the emitter
// runs subscribers synchronously to keep the assert/retract observation
// ordering deterministic.
type Subscriber func(ctx context.Context, ev Event)

// EventEmitter is a small fan-out helper that FactStore implementations can
// embed (or compose) to gain Subscribe/emit. Subscribers fire in registration
// order. Safe for concurrent Subscribe; emit holds the lock only long enough
// to snapshot the subscriber slice.
type EventEmitter struct {
	mu   sync.RWMutex
	subs []Subscriber
}

// Subscribe registers a subscriber. The returned function unsubscribes; calling
// it after the emitter has been collected is a no-op.
func (e *EventEmitter) Subscribe(s Subscriber) (unsubscribe func()) {
	e.mu.Lock()
	idx := len(e.subs)
	e.subs = append(e.subs, s)
	e.mu.Unlock()
	return func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if idx < len(e.subs) {
			e.subs[idx] = nil
		}
	}
}

// Emit fans an event out to every live subscriber, in registration order.
// Exported so that store implementations in other packages can call it.
func (e *EventEmitter) Emit(ctx context.Context, ev Event) {
	e.mu.RLock()
	snapshot := make([]Subscriber, len(e.subs))
	copy(snapshot, e.subs)
	e.mu.RUnlock()
	for _, s := range snapshot {
		if s != nil {
			s(ctx, ev)
		}
	}
}

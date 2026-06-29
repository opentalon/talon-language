package talondb_test

import (
	"context"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/factstore"
	"github.com/opentalon/talon-language/internal/reactive"
	adapterpkg "github.com/opentalon/talon-language/internal/talondb"

	"github.com/opentalon/talon-db/bboltstore"
	"github.com/opentalon/talon-db/grpcserver"
	pb "github.com/opentalon/talon-db/proto/talondbpb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const bridgeBufSize = 1 << 20

// dialBridge wires up: real bboltstore → grpcserver behind bufconn →
// pb.TalonDBServiceClient → adapterpkg.Adapter. Returns the adapter
// plus a "dropper" that writes facts directly into the server (via a
// raw gRPC client) so tests can trigger events without going through
// the adapter's own Assert (which already works in unit tests).
func dialBridge(t *testing.T) (*adapterpkg.Adapter, pb.TalonDBServiceClient, func()) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "bridge.db")
	store, err := bboltstore.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	lis := bufconn.Listen(bridgeBufSize)
	srv := grpc.NewServer()
	pb.RegisterTalonDBServiceServer(srv, grpcserver.New(store, store.Events(), "test"))
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return lis.Dial()
		}))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	rawSvc := pb.NewTalonDBServiceClient(conn)
	client := adapterpkg.NewClientFromService(rawSvc)
	adapter := adapterpkg.New(client)

	return adapter, rawSvc, func() {
		_ = conn.Close()
		srv.GracefulStop()
		_ = store.Close()
	}
}

// drainEvents reads events into a slice for n milliseconds and
// returns them. Used to give the bridge goroutine a moment to deliver
// after a write.
func drainEvents(emitter *factstore.EventEmitter, window time.Duration) (*sync.Mutex, *[]factstore.Event, func()) {
	var mu sync.Mutex
	var got []factstore.Event
	unsub := emitter.Subscribe(func(_ context.Context, ev factstore.Event) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	})
	return &mu, &got, unsub
}

func waitFor(mu *sync.Mutex, got *[]factstore.Event, want int, deadline time.Duration) bool {
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		mu.Lock()
		n := len(*got)
		mu.Unlock()
		if n >= want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestBridgeAssertEmitsPerAttributeEvents(t *testing.T) {
	t.Parallel()
	adapter, raw, cleanup := dialBridge(t)
	defer cleanup()

	bridgeCtx, cancelBridge := context.WithCancel(context.Background())
	defer cancelBridge()
	go func() { _ = adapter.RunEventBridge(bridgeCtx, "", "") }()

	emitter := adapter.Events()
	mu, got, unsub := drainEvents(emitter, 0)
	defer unsub()

	// Wait briefly for the Subscribe stream to register on the server.
	time.Sleep(150 * time.Millisecond)

	// Write a multi-attribute doc directly to the server.
	if _, err := raw.Put(context.Background(), &pb.PutRequest{
		EntityId: "tenant-a",
		DocId:    "doc-1",
		Doc:      []byte(`{":record/type":"item",":attr/km":45000}`),
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if !waitFor(mu, got, 2, 2*time.Second) {
		t.Fatalf("expected 2 events (one per attribute), got %d: %v", len(*got), *got)
	}

	mu.Lock()
	defer mu.Unlock()
	seenType, seenKm := false, false
	for _, ev := range *got {
		if ev.Kind != factstore.EventAssert {
			t.Errorf("unexpected event kind: %v", ev.Kind)
		}
		if ev.Fact.RecordID != "doc-1" || ev.Fact.Entity != "tenant-a" {
			t.Errorf("event addr wrong: %+v", ev.Fact)
		}
		switch ev.Fact.Attribute {
		case ":record/type":
			seenType = true
			if ev.Fact.Value != "item" {
				t.Errorf(":record/type value = %v", ev.Fact.Value)
			}
		case ":attr/km":
			seenKm = true
			if v, _ := ev.Fact.Value.(float64); v != 45000 {
				t.Errorf(":attr/km value = %v", ev.Fact.Value)
			}
		}
	}
	if !seenType || !seenKm {
		t.Fatalf("missing expected attributes; saw events: %v", *got)
	}
}

func TestBridgeChangeEmitsPerAttributeDelta(t *testing.T) {
	t.Parallel()
	adapter, raw, cleanup := dialBridge(t)
	defer cleanup()

	bridgeCtx, cancelBridge := context.WithCancel(context.Background())
	defer cancelBridge()
	go func() { _ = adapter.RunEventBridge(bridgeCtx, "", "") }()

	emitter := adapter.Events()
	// Subscribe BEFORE the first write so we don't race the bridge.
	mu, got, unsub := drainEvents(emitter, 0)
	defer unsub()

	// Give the bridge a moment to open its Subscribe stream server-side.
	time.Sleep(150 * time.Millisecond)

	ctx := context.Background()
	// Initial write.
	if _, err := raw.Put(ctx, &pb.PutRequest{
		EntityId: "tenant-a", DocId: "doc-1",
		Doc: []byte(`{":record/status":"active",":attr/km":1000}`),
	}); err != nil {
		t.Fatalf("Put 1: %v", err)
	}
	if !waitFor(mu, got, 2, 2*time.Second) {
		t.Fatalf("did not receive initial 2 asserts: %v", *got)
	}
	// Reset for the next phase.
	mu.Lock()
	*got = nil
	mu.Unlock()

	if _, err := raw.Put(ctx, &pb.PutRequest{
		EntityId: "tenant-a", DocId: "doc-1",
		Doc: []byte(`{":record/status":"active",":attr/km":2000,":attr/name":"Truck A"}`),
	}); err != nil {
		t.Fatalf("Put 2: %v", err)
	}
	mu2, got2 := mu, got

	if !waitFor(mu2, got2, 2, 2*time.Second) {
		t.Fatalf("expected ≥2 delta events, got %d: %v", len(*got2), *got2)
	}

	mu2.Lock()
	defer mu2.Unlock()
	gotChange, gotAssert := false, false
	for _, ev := range *got2 {
		switch ev.Fact.Attribute {
		case ":attr/km":
			if ev.Kind != factstore.EventChange {
				t.Errorf(":attr/km should be Change, got %v", ev.Kind)
			}
			if v, _ := ev.Fact.Value.(float64); v != 2000 {
				t.Errorf("new km = %v", ev.Fact.Value)
			}
			if v, _ := ev.Prev.Value.(float64); v != 1000 {
				t.Errorf("prev km = %v", ev.Prev.Value)
			}
			gotChange = true
		case ":attr/name":
			if ev.Kind != factstore.EventAssert {
				t.Errorf(":attr/name should be Assert, got %v", ev.Kind)
			}
			gotAssert = true
		case ":record/status":
			t.Errorf(":record/status should not have emitted (value unchanged), got %v", ev.Kind)
		}
	}
	if !gotChange || !gotAssert {
		t.Fatalf("missing expected delta events; saw: %v", *got2)
	}
}

func TestBridgeRetractEmitsPerAttribute(t *testing.T) {
	t.Parallel()
	adapter, raw, cleanup := dialBridge(t)
	defer cleanup()

	bridgeCtx, cancelBridge := context.WithCancel(context.Background())
	defer cancelBridge()
	go func() { _ = adapter.RunEventBridge(bridgeCtx, "", "") }()

	emitter := adapter.Events()
	mu, got, unsub := drainEvents(emitter, 0)
	defer unsub()

	time.Sleep(150 * time.Millisecond)

	ctx := context.Background()
	if _, err := raw.Put(ctx, &pb.PutRequest{
		EntityId: "tenant-a", DocId: "doc-1",
		Doc: []byte(`{":record/type":"item",":attr/km":1000}`),
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !waitFor(mu, got, 2, 2*time.Second) {
		t.Fatalf("initial asserts: got %d, want 2", len(*got))
	}
	mu.Lock()
	*got = nil
	mu.Unlock()

	if _, err := raw.Delete(ctx, &pb.DeleteRequest{
		EntityId: "tenant-a", DocId: "doc-1",
	}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	mu2, got2 := mu, got

	if !waitFor(mu2, got2, 2, 2*time.Second) {
		t.Fatalf("expected 2 retract events, got %d: %v", len(*got2), *got2)
	}

	mu2.Lock()
	defer mu2.Unlock()
	for _, ev := range *got2 {
		if ev.Kind != factstore.EventRetract {
			t.Errorf("Kind = %v, want Retract", ev.Kind)
		}
	}
}

func TestBridgeIntoReactiveDispatcher(t *testing.T) {
	// End-to-end: bbolt store → bridge → adapter.Events() → reactive
	// dispatcher → ActionHandler fired with the right OnBlock.
	t.Parallel()
	adapter, raw, cleanup := dialBridge(t)
	defer cleanup()

	bridgeCtx, cancelBridge := context.WithCancel(context.Background())
	defer cancelBridge()
	go func() { _ = adapter.RunEventBridge(bridgeCtx, "", "") }()

	var mu sync.Mutex
	var fired []*ast.OnBlock
	d := reactive.New(func(_ context.Context, b *ast.OnBlock, _ factstore.Event) {
		mu.Lock()
		fired = append(fired, b)
		mu.Unlock()
	})

	activityBlock := &ast.OnBlock{Trigger: "assert", FactType: "activity"}
	itemBlock := &ast.OnBlock{Trigger: "assert", FactType: "item"}
	d.Register(activityBlock)
	d.Register(itemBlock)
	d.Subscribe(adapter.Events())

	// Bridge needs a moment to open its stream.
	time.Sleep(150 * time.Millisecond)

	if _, err := raw.Put(context.Background(), &pb.PutRequest{
		EntityId: "tenant-a", DocId: "rec-1",
		Doc: []byte(`{"type":"activity"}`),
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(fired)
		mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("dispatcher never fired (got %d)", n)
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(fired) != 1 || fired[0] != activityBlock {
		t.Fatalf("fired = %v, want [activityBlock]", fired)
	}
}

func TestBridgeCancelStopsBridge(t *testing.T) {
	t.Parallel()
	adapter, _, cleanup := dialBridge(t)
	defer cleanup()

	bridgeCtx, cancelBridge := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- adapter.RunEventBridge(bridgeCtx, "", "") }()

	time.Sleep(100 * time.Millisecond)
	cancelBridge()

	select {
	case err := <-done:
		if err == nil || (err != context.Canceled && err != context.DeadlineExceeded) {
			t.Fatalf("RunEventBridge returned %v, want ctx error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunEventBridge did not return after context cancel")
	}
}

package reactive

import (
	"context"
	"sync"
	"testing"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/factstore"
)

// These tests exercise the end-to-end path
// MemoryStore.Assert → EventEmitter → Dispatcher → ActionHandler. The
// existing dispatcher_test.go cases feed synthetic events directly into
// the emitter; this file fills the gap acceptance-criterion-2 from
// opentalon/talon-db#20 calls out: prove `on assert` and `on change`
// dispatch fires when triggered by a real MemoryStore.Assert call.

// captureHandler returns an ActionHandler that records every (block,
// event) pair. The returned mutex + slice can be inspected after
// MemoryStore.Assert returns; emission happens synchronously so no
// extra sync is needed before reading.
func captureHandler() (*sync.Mutex, *[]fired, ActionHandler) {
	var mu sync.Mutex
	var got []fired
	return &mu, &got, func(_ context.Context, b *ast.OnBlock, ev factstore.Event) {
		mu.Lock()
		got = append(got, fired{block: b, ev: ev})
		mu.Unlock()
	}
}

func TestEndToEndAssertFiresOnAssertBlock(t *testing.T) {
	m := factstore.NewMemoryStore()
	mu, got, h := captureHandler()
	d := New(h)
	d.Register(&ast.OnBlock{Trigger: "assert", FactType: "activity"})
	d.Register(&ast.OnBlock{Trigger: "assert", FactType: "item"})
	unsub := d.Subscribe(m.Events())
	defer unsub()

	if err := m.Assert(context.Background(), []factstore.Fact{
		{RecordID: "1", Attribute: "type", Value: "activity"},
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 1 {
		t.Fatalf("got %d fires, want 1: %v", len(*got), *got)
	}
	if (*got)[0].block.FactType != "activity" {
		t.Fatalf("wrong block fired: %+v", (*got)[0].block)
	}
	if (*got)[0].ev.Kind != factstore.EventAssert {
		t.Fatalf("wrong event kind: %v", (*got)[0].ev.Kind)
	}
}

func TestEndToEndChangeFiresOnChangeBlock(t *testing.T) {
	m := factstore.NewMemoryStore()
	ctx := context.Background()

	if err := m.Assert(ctx, []factstore.Fact{
		{RecordID: "1", Attribute: "current_stock", Value: 10},
	}); err != nil {
		t.Fatalf("Assert seed: %v", err)
	}

	// Subscribe AFTER the seed so the initial assert isn't observed.
	mu, got, h := captureHandler()
	d := New(h)
	stock := &ast.OnBlock{Trigger: "change", Attr: "current_stock"}
	d.Register(stock)
	d.Register(&ast.OnBlock{Trigger: "change", Attr: "status"}) // shouldn't fire
	unsub := d.Subscribe(m.Events())
	defer unsub()

	if err := m.Assert(ctx, []factstore.Fact{
		{RecordID: "1", Attribute: "current_stock", Value: 7},
	}); err != nil {
		t.Fatalf("Assert update: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 1 {
		t.Fatalf("got %d fires, want 1: %v", len(*got), *got)
	}
	if (*got)[0].block != stock {
		t.Fatalf("wrong block fired: %+v", (*got)[0].block)
	}
	if (*got)[0].ev.Kind != factstore.EventChange {
		t.Fatalf("wrong event kind: %v", (*got)[0].ev.Kind)
	}
	if (*got)[0].ev.Prev.Value != 10 {
		t.Fatalf("Prev not carried through: %+v", (*got)[0].ev.Prev)
	}
}

func TestEndToEndIdempotentAssertFiresNothing(t *testing.T) {
	m := factstore.NewMemoryStore()
	ctx := context.Background()
	if err := m.Assert(ctx, []factstore.Fact{
		{RecordID: "1", Attribute: "current_stock", Value: 10},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	mu, got, h := captureHandler()
	d := New(h)
	d.Register(&ast.OnBlock{Trigger: "assert", FactType: ""})  // matches any assert
	d.Register(&ast.OnBlock{Trigger: "change", Attr: ""})       // matches any change
	d.Subscribe(m.Events())

	// Re-assert identical value: nothing should fire.
	if err := m.Assert(ctx, []factstore.Fact{
		{RecordID: "1", Attribute: "current_stock", Value: 10},
	}); err != nil {
		t.Fatalf("Assert idempotent: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 0 {
		t.Fatalf("idempotent assert fired %d times, want 0: %v", len(*got), *got)
	}
}

func TestEndToEndBatchAssertFiresInOrder(t *testing.T) {
	m := factstore.NewMemoryStore()
	mu, got, h := captureHandler()
	d := New(h)
	activity := &ast.OnBlock{Trigger: "assert", FactType: "activity"}
	item := &ast.OnBlock{Trigger: "assert", FactType: "item"}
	d.Register(activity)
	d.Register(item)
	d.Subscribe(m.Events())

	if err := m.Assert(context.Background(), []factstore.Fact{
		{RecordID: "1", Attribute: "type", Value: "item"},
		{RecordID: "2", Attribute: "type", Value: "activity"},
		{RecordID: "3", Attribute: "type", Value: "item"},
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 3 {
		t.Fatalf("got %d fires, want 3: %v", len(*got), *got)
	}
	want := []*ast.OnBlock{item, activity, item}
	for i, w := range want {
		if (*got)[i].block != w {
			t.Errorf("fires[%d] = %+v, want %+v", i, (*got)[i].block, w)
		}
	}
}

func TestEndToEndAssertDoesNotFireOnChangeBlock(t *testing.T) {
	m := factstore.NewMemoryStore()
	mu, got, h := captureHandler()
	d := New(h)
	d.Register(&ast.OnBlock{Trigger: "change", Attr: "current_stock"})
	d.Subscribe(m.Events())

	// First write of an attribute is an Assert event, not a Change.
	if err := m.Assert(context.Background(), []factstore.Fact{
		{RecordID: "1", Attribute: "current_stock", Value: 10},
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 0 {
		t.Fatalf("change block fired on first-time assert, got %d fires", len(*got))
	}
}

func TestEndToEndRetractStillFires(t *testing.T) {
	// Sanity check that the new emission code didn't break the existing
	// retract path.
	m := factstore.NewMemoryStore()
	if err := m.Assert(context.Background(), []factstore.Fact{
		{RecordID: "1", Attribute: "type", Value: "item"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	mu, got, h := captureHandler()
	d := New(h)
	d.Register(&ast.OnBlock{Trigger: "retract", FactType: "item"})
	d.Subscribe(m.Events())

	if err := m.Retract(context.Background(), factstore.RetractPattern{
		RecordID: "1", Attribute: "type", Value: "item",
	}); err != nil {
		t.Fatalf("Retract: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 1 {
		t.Fatalf("got %d fires, want 1: %v", len(*got), *got)
	}
	if (*got)[0].ev.Kind != factstore.EventRetract {
		t.Fatalf("wrong kind: %v", (*got)[0].ev.Kind)
	}
}

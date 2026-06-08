package reactive

import (
	"context"
	"testing"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/factstore"
)

type fired struct {
	block *ast.OnBlock
	ev    factstore.Event
}

func TestDispatcherChangeMatchesByAttr(t *testing.T) {
	var got []fired
	d := New(func(_ context.Context, b *ast.OnBlock, ev factstore.Event) {
		got = append(got, fired{b, ev})
	})
	stock := &ast.OnBlock{Trigger: "change", Attr: "current_stock"}
	status := &ast.OnBlock{Trigger: "change", Attr: "status"}
	d.Register(stock)
	d.Register(status)

	var emitter factstore.EventEmitter
	unsubscribe := d.Subscribe(&emitter)
	defer unsubscribe()

	emitter.Emit(context.Background(), factstore.Event{
		Kind: factstore.EventChange,
		Fact: factstore.Fact{Attribute: "current_stock", Value: 10},
		Prev: factstore.Fact{Attribute: "current_stock", Value: 20},
	})

	if len(got) != 1 || got[0].block != stock {
		t.Fatalf("expected only the stock block to fire, got %v", got)
	}
}

func TestDispatcherAssertMatchesByFactType(t *testing.T) {
	var got []*ast.OnBlock
	d := New(func(_ context.Context, b *ast.OnBlock, _ factstore.Event) {
		got = append(got, b)
	})
	activity := &ast.OnBlock{Trigger: "assert", FactType: "activity"}
	item := &ast.OnBlock{Trigger: "assert", FactType: "item"}
	d.Register(activity)
	d.Register(item)

	var emitter factstore.EventEmitter
	d.Subscribe(&emitter)

	// A new activity record asserts a "type=activity" fact.
	emitter.Emit(context.Background(), factstore.Event{
		Kind: factstore.EventAssert,
		Fact: factstore.Fact{Attribute: "type", Value: "activity"},
	})

	if len(got) != 1 || got[0] != activity {
		t.Fatalf("expected the activity block to fire, got %v", got)
	}
}

func TestDispatcherIgnoresMismatchedKind(t *testing.T) {
	var fires int
	d := New(func(_ context.Context, _ *ast.OnBlock, _ factstore.Event) { fires++ })
	d.Register(&ast.OnBlock{Trigger: "change", Attr: "x"})

	var emitter factstore.EventEmitter
	d.Subscribe(&emitter)

	emitter.Emit(context.Background(), factstore.Event{
		Kind: factstore.EventAssert,
		Fact: factstore.Fact{Attribute: "x", Value: 1},
	})

	if fires != 0 {
		t.Fatalf("expected change-block not to fire on assert event, got %d fires", fires)
	}
}

func TestDispatcherRetract(t *testing.T) {
	var got *ast.OnBlock
	d := New(func(_ context.Context, b *ast.OnBlock, _ factstore.Event) { got = b })
	b := &ast.OnBlock{Trigger: "retract", FactType: "item"}
	d.Register(b)

	var emitter factstore.EventEmitter
	d.Subscribe(&emitter)

	emitter.Emit(context.Background(), factstore.Event{
		Kind: factstore.EventRetract,
		Fact: factstore.Fact{Attribute: "type", Value: "item"},
	})

	if got != b {
		t.Fatalf("expected retract block to fire, got %v", got)
	}
}

func TestEmitterUnsubscribeStopsDelivery(t *testing.T) {
	var fires int
	var emitter factstore.EventEmitter
	unsubscribe := emitter.Subscribe(func(_ context.Context, _ factstore.Event) { fires++ })

	emitter.Emit(context.Background(), factstore.Event{Kind: factstore.EventAssert})
	unsubscribe()
	emitter.Emit(context.Background(), factstore.Event{Kind: factstore.EventAssert})

	if fires != 1 {
		t.Fatalf("expected exactly one delivery before unsubscribe, got %d", fires)
	}
}

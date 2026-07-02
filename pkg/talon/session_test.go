package talon_test

import (
	"context"
	"testing"

	"github.com/opentalon/talon-language/pkg/talon"
)

// newCaller returns a mockCaller (defined in api_test.go) that answers
// every MCP call with a canned success result so downstream steps resolve.
func newCaller() *mockCaller {
	return &mockCaller{handler: func(_, _ string, _ map[string]any) (any, error) {
		return map[string]any{"status": "ok", "id": "ord-1"}, nil
	}}
}

const refillSrc = `
on change attr "current_stock" to 0 {
  logger.warn "stock-out for {event.entity}"
  workflow "Refill stock"
}
workflow "Refill stock" {
  step "create_refill" {
    mcp "timly" "create-order" { item_id step("trigger").result.entity  quantity 50 }
  }
}`

func TestSession_FiresWorkflowWithTriggerPresets(t *testing.T) {
	caller := newCaller()
	s, err := talon.NewSession(refillSrc, talon.WithMCP(caller))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	// Establish the item at stock 3 — no crossing, no firing.
	firings, err := s.Assert(context.Background(), []talon.Fact{
		{RecordID: "42", Attribute: "current_stock", Value: 3},
	})
	if err != nil {
		t.Fatalf("Assert(3): %v", err)
	}
	if len(firings) != 0 {
		t.Fatalf("stock 3 should not fire, got %d firings", len(firings))
	}

	// Drop to 0 — the `to 0` guard matches and the workflow fires once.
	firings, err = s.Assert(context.Background(), []talon.Fact{
		{RecordID: "42", Attribute: "current_stock", Value: 0},
	})
	if err != nil {
		t.Fatalf("Assert(0): %v", err)
	}
	if len(firings) != 1 {
		t.Fatalf("stock 0 should fire once, got %d firings: %+v", len(firings), firings)
	}
	f := firings[0]
	if f.RefKind != "workflow" || f.Ref != "Refill stock" {
		t.Errorf("firing ref: got %q/%q", f.RefKind, f.Ref)
	}
	if f.Err != nil {
		t.Errorf("firing error: %v", f.Err)
	}
	if f.Result == nil || f.Result.Blocks["Refill stock"] == nil {
		t.Fatalf("firing result missing: %+v", f.Result)
	}

	// The workflow's mcp step ran with the trigger's entity threaded in.
	if len(caller.calls) != 1 {
		t.Fatalf("expected 1 mcp call, got %d", len(caller.calls))
	}
	got := caller.calls[0]
	if got.Server != "timly" || got.Tool != "create-order" {
		t.Errorf("mcp call: got %s/%s", got.Server, got.Tool)
	}
	if got.Args["item_id"] != "42" {
		t.Errorf("item_id from step(\"trigger\").result.entity: got %v, want \"42\"", got.Args["item_id"])
	}
}

func TestSession_IdempotentReassertNoFiring(t *testing.T) {
	caller := newCaller()
	s, err := talon.NewSession(refillSrc, talon.WithMCP(caller))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	// Establish stock 3 (assert), then drop to 0 (change) — fires once.
	if _, err := s.Assert(ctx, []talon.Fact{{RecordID: "42", Attribute: "current_stock", Value: 3}}); err != nil {
		t.Fatalf("Assert(3): %v", err)
	}
	if _, err := s.Assert(ctx, []talon.Fact{{RecordID: "42", Attribute: "current_stock", Value: 0}}); err != nil {
		t.Fatalf("Assert(0): %v", err)
	}
	// Re-assert the same value: unchanged → no event → no firing.
	firings, err := s.Assert(ctx, []talon.Fact{{RecordID: "42", Attribute: "current_stock", Value: 0}})
	if err != nil {
		t.Fatalf("re-Assert(0): %v", err)
	}
	if len(firings) != 0 {
		t.Fatalf("re-assert of unchanged value should not fire, got %d", len(firings))
	}
	if len(caller.calls) != 1 {
		t.Fatalf("workflow should have run exactly once across the asserts, got %d", len(caller.calls))
	}
}

func TestSession_SnapshotHydrationNoReplayFiring(t *testing.T) {
	ctx := context.Background()

	// Session A brings an item from 3 down to 0 (one firing), then snapshots.
	callerA := newCaller()
	a, err := talon.NewSession(refillSrc, talon.WithMCP(callerA))
	if err != nil {
		t.Fatalf("NewSession A: %v", err)
	}
	if _, err := a.Assert(ctx, []talon.Fact{{RecordID: "42", Attribute: "current_stock", Value: 3}}); err != nil {
		t.Fatalf("A Assert(3): %v", err)
	}
	if _, err := a.Assert(ctx, []talon.Fact{{RecordID: "42", Attribute: "current_stock", Value: 0}}); err != nil {
		t.Fatalf("A Assert(0): %v", err)
	}
	if len(callerA.calls) != 1 {
		t.Fatalf("session A should have fired once, got %d", len(callerA.calls))
	}
	snap := a.Snapshot()
	a.Close()

	// Rebuild: hydrate a fresh store from the snapshot BEFORE NewSession
	// subscribes, then start session B.
	store := talon.NewMemoryStore()
	var hydrate []talon.Fact
	for id, attrs := range snap {
		for attr, val := range attrs {
			hydrate = append(hydrate, talon.Fact{RecordID: itoa(id), Attribute: attr, Value: val})
		}
	}
	if err := store.Assert(ctx, hydrate); err != nil {
		t.Fatalf("hydrate: %v", err)
	}

	callerB := newCaller()
	b, err := talon.NewSession(refillSrc, talon.WithMCP(callerB), talon.WithFactStore(store))
	if err != nil {
		t.Fatalf("NewSession B: %v", err)
	}
	defer b.Close()

	// Replaying stock 0 (already the state) must not re-fire.
	firings, err := b.Assert(ctx, []talon.Fact{{RecordID: "42", Attribute: "current_stock", Value: 0}})
	if err != nil {
		t.Fatalf("B Assert(0): %v", err)
	}
	if len(firings) != 0 {
		t.Fatalf("replay of hydrated state should not fire, got %d", len(firings))
	}
	if len(callerB.calls) != 0 {
		t.Fatalf("no workflow should run on restart replay, got %d", len(callerB.calls))
	}
}

func TestSession_WhenThresholdCrossing(t *testing.T) {
	src := `
on change attr "current_stock" {
  when prev_value >= 5 and new_value < 5
  workflow "Reorder"
}
workflow "Reorder" {
  step "s" { mcp "timly" "create-order" { quantity 10 } }
}`
	caller := newCaller()
	s, err := talon.NewSession(src, talon.WithMCP(caller))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	// Seed at 8 (no prior value → assert, not change): no firing.
	if f, _ := s.Assert(ctx, []talon.Fact{{RecordID: "1", Attribute: "current_stock", Value: 8}}); len(f) != 0 {
		t.Fatalf("initial assert should not fire change, got %d", len(f))
	}
	// 8 → 6: still >= 5, no crossing.
	if f, _ := s.Assert(ctx, []talon.Fact{{RecordID: "1", Attribute: "current_stock", Value: 6}}); len(f) != 0 {
		t.Fatalf("8→6 should not fire, got %d", len(f))
	}
	// 6 → 4: crosses below 5, fires once.
	f, err := s.Assert(ctx, []talon.Fact{{RecordID: "1", Attribute: "current_stock", Value: 4}})
	if err != nil {
		t.Fatalf("Assert(4): %v", err)
	}
	if len(f) != 1 {
		t.Fatalf("6→4 should fire once, got %d", len(f))
	}
	// 4 → 3: prev_value (4) already < 5, guard's prev>=5 fails → no fire.
	if f, _ := s.Assert(ctx, []talon.Fact{{RecordID: "1", Attribute: "current_stock", Value: 3}}); len(f) != 0 {
		t.Fatalf("4→3 should not re-fire, got %d", len(f))
	}
}

func TestSession_RejectsCrossFactWhen(t *testing.T) {
	src := `
on change attr "current_stock" {
  when new_value <= attr "minimum_amount"
  workflow "W"
}
workflow "W" { step "s" { mcp "a" "b" {} } }`
	_, err := talon.NewSession(src)
	if err == nil {
		t.Fatal("expected NewSession to reject cross-fact when clause")
	}
	ce, ok := err.(*talon.CompileError)
	if !ok {
		t.Fatalf("expected *CompileError, got %T: %v", err, err)
	}
	if ce.Stage != "validate" {
		t.Errorf("Stage: got %q, want validate", ce.Stage)
	}
}

func TestSession_LoggerOnlyBodyFiresWithEmptyRef(t *testing.T) {
	src := `
on assert item {
  logger.info "new item {event.entity}"
}`
	s, err := talon.NewSession(src)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()
	firings, err := s.Assert(context.Background(), []talon.Fact{
		{RecordID: "7", Attribute: "type", Value: "item"},
	})
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if len(firings) != 1 {
		t.Fatalf("logger-only on-block should record one firing, got %d", len(firings))
	}
	if firings[0].Ref != "" || firings[0].RefKind != "" || firings[0].Result != nil {
		t.Errorf("logger-only firing should have empty ref/result: %+v", firings[0])
	}
}

func TestSession_RunAll(t *testing.T) {
	src := `
workflow "Ping" {
  step "s" { mcp "svc" "ping" {} }
}`
	caller := newCaller()
	s, err := talon.NewSession(src, talon.WithMCP(caller))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()
	res, err := s.RunAll(context.Background())
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}
	if res.Blocks["Ping"] == nil {
		t.Fatalf("RunAll should have run the Ping workflow: %+v", res.Blocks)
	}
	if len(caller.calls) != 1 {
		t.Errorf("RunAll should have made 1 mcp call, got %d", len(caller.calls))
	}
}

// itoa avoids importing strconv just for the snapshot hydration test.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

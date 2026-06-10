package executor

import (
	"context"
	"strconv"
	"testing"

	"github.com/opentalon/talon-language/internal/factstore"
	"github.com/opentalon/talon-language/internal/lexer"
	"github.com/opentalon/talon-language/internal/parser"
	"github.com/opentalon/talon-language/internal/planner"
)

// compileSrc lexes + parses + plans a Talon snippet. Test helper —
// returns the plan map keyed by block name.
func compileSrc(t *testing.T, src string) map[string]*planner.QueryPlan {
	t.Helper()
	tokens, ld := lexer.Lex("sm_test.talon", src)
	if ld.HasErrors() {
		t.Fatalf("lex: %v", ld)
	}
	prog, pd := parser.Parse("sm_test.talon", tokens)
	if pd.HasErrors() {
		t.Fatalf("parse: %v", pd)
	}
	plans, planDiags := planner.Plan(prog)
	if planDiags.HasErrors() {
		t.Fatalf("plan: %v", planDiags)
	}
	return plans
}

// seedEntity asserts a record + its attrs into a MemoryStore in a
// single Assert call.
func seedEntity(t *testing.T, store *factstore.MemoryStore, id int, attrs map[string]any) {
	t.Helper()
	idStr := strconv.Itoa(id)
	facts := []factstore.Fact{
		{RecordID: idStr, Attribute: ":record/type", Value: attrs["__type"]},
	}
	for k, v := range attrs {
		if k == "__type" {
			continue
		}
		facts = append(facts, factstore.Fact{
			RecordID:  idStr,
			Attribute: ":attr/" + k,
			Value:     v,
		})
	}
	if err := store.Assert(context.Background(), facts); err != nil {
		t.Fatalf("seed entity %d: %v", id, err)
	}
}

func currentState(t *testing.T, store *factstore.MemoryStore, id int) string {
	t.Helper()
	snap := store.Snapshot()
	if attrs, ok := snap[id]; ok {
		if v, ok := attrs[":record/state"].(string); ok {
			return v
		}
	}
	return ""
}

func TestStateMachine_TransitionFires(t *testing.T) {
	src := `
state_machine "Order lifecycle" {
  for records where type == "order"
  states pending, approved
  initial pending
  transition pending -> approved when attr "amount" > 1000
}
`
	plans := compileSrc(t, src)
	plan := plans["Order lifecycle"]
	if plan == nil {
		t.Fatal("no plan for state_machine block")
	}

	store := factstore.NewMemoryStore()
	ctx := context.Background()

	// Seed two entities, both in pending. Only #101 has amount > 1000;
	// the transition should fire only on it.
	seedEntity(t, store, 100, map[string]any{
		"__type": "order",
		"state":  "pending",   // raw — not ":record/state"-prefixed
		"amount": 500.0,
	})
	seedEntity(t, store, 101, map[string]any{
		"__type": "order",
		"state":  "pending",
		"amount": 2500.0,
	})
	// Re-assert the state at ":record/state" so the planner-built
	// pattern can find it. seedEntity put state at ":attr/state".
	_ = store.Assert(ctx, []factstore.Fact{
		{RecordID: "100", Attribute: ":record/state", Value: "pending"},
		{RecordID: "101", Attribute: ":record/state", Value: "pending"},
	})

	ex := NewExecutor(store)
	blocks, err := ex.RunAll(ctx, plans)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	res := blocks["Order lifecycle"]
	if res == nil {
		t.Fatal("no block result")
	}
	outcomes, ok := res.Vars["sm_result"].([]StateMachineOutcome)
	if !ok {
		t.Fatalf("sm_result type = %T, want []StateMachineOutcome", res.Vars["sm_result"])
	}
	if len(outcomes) != 2 {
		t.Fatalf("outcomes = %d, want 2", len(outcomes))
	}

	for _, o := range outcomes {
		switch o.EntityID {
		case 100:
			if o.Fired || o.ToState != "pending" {
				t.Errorf("entity 100: expected no transition, got %+v", o)
			}
		case 101:
			if !o.Fired || o.ToState != "approved" {
				t.Errorf("entity 101: expected pending→approved, got %+v", o)
			}
		}
	}

	// Side-effect check: the FactStore should now hold 101 in approved.
	if s := currentState(t, store, 101); s != "approved" {
		t.Errorf("entity 101 state after run: %q, want approved", s)
	}
	if s := currentState(t, store, 100); s != "pending" {
		t.Errorf("entity 100 state after run: %q, want pending (unchanged)", s)
	}
}

func TestStateMachine_FirstGuardWins(t *testing.T) {
	// Two transitions out of the same state; both guards satisfy.
	// First in source order should fire, second is skipped.
	src := `
state_machine "Priority order" {
  for records where type == "task"
  states todo, urgent, normal
  initial todo
  transition todo -> urgent when attr "priority" > 5
  transition todo -> normal when attr "priority" > 0
}
`
	plans := compileSrc(t, src)
	store := factstore.NewMemoryStore()
	ctx := context.Background()
	seedEntity(t, store, 1, map[string]any{
		"__type":   "task",
		"priority": 7.0,
	})
	_ = store.Assert(ctx, []factstore.Fact{
		{RecordID: "1", Attribute: ":record/state", Value: "todo"},
	})

	ex := NewExecutor(store)
	_, err := ex.RunAll(ctx, plans)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if s := currentState(t, store, 1); s != "urgent" {
		t.Errorf("first-guard-wins broken: state = %q, want urgent", s)
	}
}

func TestStateMachine_InvariantRecordsViolation(t *testing.T) {
	src := `
state_machine "Shipping" {
  for records where type == "order"
  states approved, shipped
  initial approved
  transition approved -> shipped when attr "ready" == true
  invariant in shipped require attr "tracking" == "filled"
}
`
	plans := compileSrc(t, src)
	store := factstore.NewMemoryStore()
	ctx := context.Background()
	seedEntity(t, store, 1, map[string]any{
		"__type":   "order",
		"ready":    true,
		"tracking": "empty", // invariant violation: not "filled"
	})
	_ = store.Assert(ctx, []factstore.Fact{
		{RecordID: "1", Attribute: ":record/state", Value: "approved"},
	})

	ex := NewExecutor(store)
	blocks, err := ex.RunAll(ctx, plans)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	outcomes := blocks["Shipping"].Vars["sm_result"].([]StateMachineOutcome)
	if len(outcomes) != 1 {
		t.Fatalf("outcomes = %d", len(outcomes))
	}
	o := outcomes[0]
	if !o.Fired || o.ToState != "shipped" {
		t.Fatalf("expected transition to shipped, got %+v", o)
	}
	if len(o.Violations) == 0 {
		t.Fatal("expected invariant violation in shipped state")
	}
	if o.Violations[0] != "shipped" {
		t.Errorf("violation state = %q, want shipped", o.Violations[0])
	}
}

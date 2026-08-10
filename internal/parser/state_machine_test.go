package parser

import (
	"testing"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/lexer"
)

func parseSrc(t *testing.T, src string) *ast.Program {
	t.Helper()
	tokens, ld := lexer.Lex("test.tln", src)
	if ld.HasErrors() {
		t.Fatalf("lex: %v", ld)
	}
	prog, pd := Parse("test.tln", tokens)
	if pd.HasErrors() {
		t.Fatalf("parse: %v", pd)
	}
	return prog
}

func TestParseStateMachine_Basic(t *testing.T) {
	src := `
state_machine "Order lifecycle" {
  for records where type == "order"
  states pending, approved, shipped, delivered, cancelled
  initial pending
  state_attr "lifecycle_state"
  transition pending -> approved when attr "amount" > 1000
  transition pending -> cancelled when attr "marked_cancelled" == true
  transition approved -> shipped when type == "order"
  invariant in shipped require type == "order"
}
`
	prog := parseSrc(t, src)
	if len(prog.Blocks) != 1 {
		t.Fatalf("want 1 block, got %d", len(prog.Blocks))
	}
	sm, ok := prog.Blocks[0].(*ast.StateMachineBlock)
	if !ok {
		t.Fatalf("want *StateMachineBlock, got %T", prog.Blocks[0])
	}

	if sm.Name != "Order lifecycle" {
		t.Errorf("name = %q", sm.Name)
	}
	wantStates := []string{"pending", "approved", "shipped", "delivered", "cancelled"}
	if len(sm.States) != len(wantStates) {
		t.Fatalf("states len = %d, want %d", len(sm.States), len(wantStates))
	}
	for i, s := range sm.States {
		if s.Name != wantStates[i] {
			t.Errorf("states[%d].Name = %q, want %q", i, s.Name, wantStates[i])
		}
	}
	if sm.Initial != "pending" {
		t.Errorf("initial = %q", sm.Initial)
	}
	if sm.StateAttr != "lifecycle_state" {
		t.Errorf("state_attr = %q", sm.StateAttr)
	}
	if len(sm.Transitions) != 3 {
		t.Fatalf("transitions = %d, want 3", len(sm.Transitions))
	}
	if sm.Transitions[0].From != "pending" || sm.Transitions[0].To != "approved" {
		t.Errorf("first transition = %s -> %s", sm.Transitions[0].From, sm.Transitions[0].To)
	}
	if sm.Transitions[0].When == nil {
		t.Error("first transition missing when condition")
	}
	if len(sm.Invariants) != 1 {
		t.Fatalf("invariants = %d, want 1", len(sm.Invariants))
	}
	if sm.Invariants[0].State != "shipped" {
		t.Errorf("invariant state = %q", sm.Invariants[0].State)
	}
}

func TestParseStateMachine_NoStateAttrDefaults(t *testing.T) {
	src := `
state_machine "Simple" {
  for records where type == "task"
  states todo, doing, done
  initial todo
  transition todo -> doing when type == "task"
  transition doing -> done when type == "task"
}
`
	prog := parseSrc(t, src)
	sm := prog.Blocks[0].(*ast.StateMachineBlock)
	if sm.StateAttr != "" {
		t.Errorf("expected empty state_attr (let planner default), got %q", sm.StateAttr)
	}
	if sm.Initial != "todo" {
		t.Errorf("initial = %q", sm.Initial)
	}
}

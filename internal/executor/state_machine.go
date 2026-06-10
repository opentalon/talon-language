package executor

import (
	"context"
	"fmt"
	"strconv"

	"github.com/opentalon/talon-language/internal/constraints"
	"github.com/opentalon/talon-language/internal/factstore"
	"github.com/opentalon/talon-language/internal/planner"
)

// StateMachineOutcome is the per-entity result of running a
// StateMachineStep. Exposed in BlockResult.Vars[Into] so callers
// (testrunner, REPL) can inspect transitions without re-querying.
type StateMachineOutcome struct {
	EntityID   int
	FromState  string
	ToState    string // == FromState when no transition fired
	Fired      bool
	Violations []string // invariant names that failed in ToState
}

// execStateMachine drives every candidate entity through one step
// of the declared FSM. For each row from the FactQuery:
//
//  1. Read the current state from row[StateColumn].
//  2. Build a flat attr-name → value map from the trailing columns
//     (planner placed them there in `Columns` order); add the
//     state attribute too so guards that reference it see it.
//  3. Walk Transitions in source order; the first whose From
//     matches and whose When guard evaluates true fires. Guard
//     evaluation goes through constraints.EvalCondition — the same
//     evaluator the testrunner uses for filter conditions, so
//     guard semantics match what the user reads elsewhere.
//  4. On fire, Assert the new state into the FactStore so
//     subsequent blocks see the post-transition value.
//  5. Run state invariants for the (now current) state; record
//     any failures as Violations on the outcome.
//
// Iteration is single-step per Run; chained transitions
// (`pending → approved → shipped` in one Run) need a fixpoint
// loop. Documented in docs/limitations.md.
func (e *Executor) execStateMachine(ctx context.Context, s *planner.StateMachineStep, vars map[string]any) (StepResult, error) {
	rows, _ := vars[s.Input].([][]any)
	outcomes := make([]StateMachineOutcome, 0, len(rows))

	stateAttrName := stripNamespace(s.StateAttr)
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		eid, ok := toIntSM(row[0])
		if !ok {
			continue
		}

		// Current state from the planner-placed StateColumn. The
		// planner guarantees this column exists in every row; if a
		// fact for the state attr doesn't exist for this entity,
		// the FactStore returns no row at all (Pattern requires
		// the attribute), so we fall back to Initial.
		curState := s.Initial
		if s.StateColumn < len(row) {
			if v, ok := row[s.StateColumn].(string); ok && v != "" {
				curState = v
			}
		}

		// Build the attr map for the guard evaluator. Columns live
		// after the state column in row order: planner appends
		// stateVar first (StateColumn), then one var per attr in
		// Columns. Add the state attribute too so a guard like
		// `when attr "state" == "approved"` resolves.
		attrs := map[string]any{stateAttrName: curState}
		startCol := s.StateColumn + 1
		for i, name := range s.Columns {
			col := startCol + i
			if col < len(row) {
				attrs[name] = row[col]
			}
		}

		out := StateMachineOutcome{EntityID: eid, FromState: curState, ToState: curState}

		for _, t := range s.Transitions {
			// Substate semantics: a transition from a parent ("a")
			// matches when the entity is in any child of that
			// parent ("a/sub1", "a/sub2"). Direct match still
			// wins when both transitions are declared.
			if !stateMatches(curState, t.From) {
				continue
			}
			if t.When != nil {
				ok, err := constraints.EvalCondition(t.When, attrs)
				if err != nil || !ok {
					continue
				}
			}
			// Fire: write new state to FactStore. First-guard-wins
			// in source order — simple to reason about, deferred
			// priority/conflict-resolution to a separate ADR if
			// real workloads need it.
			err := e.Client.Assert(ctx, []factstore.Fact{{
				RecordID:  strconv.Itoa(eid),
				Attribute: s.StateAttr,
				Value:     t.To,
			}})
			if err != nil {
				return StepResult{}, fmt.Errorf("state_machine: write transition for entity %d: %w", eid, err)
			}
			out.ToState = t.To
			out.Fired = true
			attrs[stateAttrName] = t.To
			break
		}

		// Invariants: evaluated against the post-transition view.
		// Treated as warning-level — record violation but don't
		// block subsequent steps. ConstraintBlock is the place for
		// reject semantics.
		for _, inv := range s.Invariants {
			if inv.State != out.ToState {
				continue
			}
			ok, err := constraints.EvalCondition(inv.Required, attrs)
			if err == nil && !ok {
				out.Violations = append(out.Violations, inv.State)
			}
		}

		outcomes = append(outcomes, out)
	}

	vars[s.Into] = outcomes
	return StepResult{
		Type:   "StateMachineStep",
		Name:   s.BlockName,
		Output: outcomes,
	}, nil
}

// stateMatches reports whether an entity in `cur` is governed by a
// transition declared `from`. Direct equality always matches; a
// parent-state transition (e.g. "in_flight" with no substate
// suffix) matches any child state ("in_flight/boarding"). Used by
// the executor to implement Harel-style outermost-matches-first
// semantics in a flat declaration syntax.
func stateMatches(cur, from string) bool {
	if cur == from {
		return true
	}
	// Parent prefix match: cur = "in_flight/boarding", from = "in_flight"
	if len(cur) > len(from) && cur[:len(from)] == from && cur[len(from)] == '/' {
		return true
	}
	return false
}

// stripNamespace removes a ":attr/" or ":record/" prefix from an
// attribute name so the EvalCondition path (which expects bare
// names like "km" or "status") gets the form it wants.
func stripNamespace(attr string) string {
	for i, r := range attr {
		if r == '/' {
			return attr[i+1:]
		}
	}
	if len(attr) > 0 && attr[0] == ':' {
		return attr[1:]
	}
	return attr
}

func toIntSM(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	}
	return 0, false
}

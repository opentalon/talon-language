package executor

import (
	"context"
	"testing"

	"github.com/opentalon/tln-language/internal/factstore"
	"github.com/opentalon/tln-language/internal/planner"
)

// TestExecutor_OptimizeGA_BudgetConstraint runs a 2-objective + 1-constraint
// subset selection end-to-end against a fake FactStore. The fixture is a
// reorder problem with a hard $5000 budget: subsets that exceed the budget
// must be infeasible (and thus excluded from the frontier).
func TestExecutor_OptimizeGA_BudgetConstraint(t *testing.T) {
	src := `
combine "Reorder picks" {
  for records where type == "stock_item" and status == "active"
  select 2 from records
  minimize total(attr "cost")
  maximize total(attr "value")
  subject_to total(attr "cost") <= 5000
  return id, cost, value
  seed 42
}`
	plan := mustPlan(t, src, "Reorder picks")

	// Rows: [entity_id, cost, value]. The cheap pair (101+102) costs 3000;
	// the expensive pair (103+104) costs 9000 — infeasible.
	rows := [][]any{
		{101, 1500.0, 30.0},
		{102, 1500.0, 28.0},
		{103, 4500.0, 50.0},
		{104, 4500.0, 49.0},
	}
	fs := &fakeStore{queryReply: func(factstore.Query) ([][]any, error) { return rows, nil }}
	e := &Executor{Client: fs}

	result, err := e.Run(context.Background(), plan)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	out, ok := result.Vars["frontier"].(map[string]any)
	if !ok {
		t.Fatalf("frontier var: want map[string]any, got %T", result.Vars["frontier"])
	}
	if fn, _ := out["function"].(string); fn != planner.FuncOptimizeGA {
		t.Errorf("function: got %q, want %q", fn, planner.FuncOptimizeGA)
	}

	subsets, _ := out["subsets"].([]map[string]any)
	if len(subsets) == 0 {
		t.Fatal("frontier empty — GA found no feasible subset")
	}

	for _, s := range subsets {
		selected, _ := s["selected"].([]int)
		values, _ := s["values"].(map[string]any)
		cost, _ := values["total(cost)"].(float64)
		if cost > 5000 {
			t.Errorf("frontier subset %v exceeds budget: cost=%v", selected, cost)
		}
		if len(selected) != 2 {
			t.Errorf("subset size: want 2, got %d (%v)", len(selected), selected)
		}
	}

	// Flagged should be the union of entity IDs across the frontier.
	flagged, _ := out["flagged_ids"].([]int)
	if len(flagged) == 0 {
		t.Error("flagged_ids empty")
	}
}

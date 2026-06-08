package executor

import (
	"context"
	"testing"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/factstore"
	"github.com/opentalon/talon-language/internal/lexer"
	"github.com/opentalon/talon-language/internal/parser"
	"github.com/opentalon/talon-language/internal/planner"
	"github.com/opentalon/talon-language/internal/validator"
)

// TestExecutor_OptimizePareto runs a 2-objective combine end-to-end against a
// fake FactStore, exercising the parse → validate → plan → execute → frontier
// pipeline. The fixture mirrors examples/fleet_dispatch.talon (cost vs urgency)
// so the test doubles as a sanity check on the example.
func TestExecutor_OptimizePareto(t *testing.T) {
	src := `
combine "Dispatch picks" {
  for records where type == "item" and status == "active"
  minimize attr "cost_per_km"
  maximize attr "urgency_score"
  return id, cost_per_km, urgency_score
}`
	plan := mustPlan(t, src, "Dispatch picks")

	// Rows produced by the Datalevin query, in the column order the planner
	// emits: [?e, ?cost_per_km, ?urgency_score]. Note: the queryBuilder may
	// also include selector-bound vars; we look up indices by name to stay
	// robust to that. Here we keep the selector free of attr-bindings so the
	// row shape is exactly [entity_id, cost, urgency].
	rows := [][]any{
		{101, 0.8, 90.0}, // frontier — interior point
		{102, 0.5, 40.0}, // frontier — cheapest
		{103, 1.2, 95.0}, // frontier — most urgent
		{104, 1.0, 50.0}, // dominated by 101
	}
	fs := &fakeStore{
		queryReply: func(factstore.Query) ([][]any, error) { return rows, nil },
	}
	e := &Executor{Client: fs}

	result, err := e.Run(context.Background(), plan)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	out, ok := result.Vars["frontier"].(map[string]any)
	if !ok {
		t.Fatalf("frontier var: want map[string]any, got %T", result.Vars["frontier"])
	}
	if fn, _ := out["function"].(string); fn != planner.FuncOptimizePareto {
		t.Errorf("function: got %q, want %q", fn, planner.FuncOptimizePareto)
	}

	frontier, _ := out["frontier"].([]map[string]any)
	if len(frontier) != 3 {
		t.Fatalf("frontier size: want 3, got %d (%v)", len(frontier), frontier)
	}

	wantOnFrontier := map[int]bool{101: true, 102: true, 103: true}
	for _, s := range frontier {
		id, _ := s["entity_id"].(int)
		if !wantOnFrontier[id] {
			t.Errorf("entity %d on frontier but not expected", id)
		}
		if rank, _ := s["rank"].(int); rank != 0 {
			t.Errorf("entity %d: rank = %d, want 0", id, rank)
		}
	}

	// 104 should appear in `all` with rank > 0 and DominatedCount > 0.
	all, _ := out["all"].([]map[string]any)
	var found104 map[string]any
	for _, s := range all {
		if id, _ := s["entity_id"].(int); id == 104 {
			found104 = s
			break
		}
	}
	if found104 == nil {
		t.Fatal("entity 104 missing from `all`")
	}
	if rank, _ := found104["rank"].(int); rank == 0 {
		t.Errorf("entity 104: rank should be > 0 (dominated by 101)")
	}
	if dom, _ := found104["dominated"].(int); dom == 0 {
		t.Errorf("entity 104: dominated count should be > 0")
	}
}

func mustPlan(t *testing.T, src, blockName string) *planner.QueryPlan {
	t.Helper()
	tokens, ld := lexer.Lex("test.talon", src)
	if ld.HasErrors() {
		t.Fatalf("lex: %v", ld)
	}
	prog, pd := parser.Parse("test.talon", tokens)
	if pd.HasErrors() {
		t.Fatalf("parse: %v", pd)
	}
	if vd := validator.Validate("test.talon", prog); vd.HasErrors() {
		t.Fatalf("validate: %v", vd)
	}
	plans, diags := planner.Plan(prog)
	if diags.HasErrors() {
		t.Fatalf("plan: %v", diags)
	}
	p, ok := plans[blockName]
	if !ok {
		t.Fatalf("no plan for %q", blockName)
	}
	return p
}

// Silence unused import warning if ast happens to drop out of use during edits.
var _ ast.Block = (*ast.CombineBlock)(nil)

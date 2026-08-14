package executor

import (
	"context"
	"testing"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/factstore"
	"github.com/opentalon/tln-language/internal/mlruntime"
	"github.com/opentalon/tln-language/internal/planner"
)

// stubGraphProvider returns a fixed snapshot regardless of options.
type stubGraphProvider struct {
	snap *factstore.GraphSnapshot
}

func (s *stubGraphProvider) Snapshot(_ context.Context, _ string, _ map[string]any) (*factstore.GraphSnapshot, error) {
	return s.snap, nil
}

func TestExecutorRunsFindRelatedPlan(t *testing.T) {
	// Build a small graph: two cliques (A,B,C) and (X,Y,Z) joined through
	// a bridge node M. Seed = A → top results should be members of A's clique.
	triples := []factstore.FactTriple{
		// Clique A
		{Entity: "1", Attribute: "tag", Value: "g1"},
		{Entity: "2", Attribute: "tag", Value: "g1"},
		{Entity: "3", Attribute: "tag", Value: "g1"},
		// Clique B
		{Entity: "4", Attribute: "tag", Value: "g2"},
		{Entity: "5", Attribute: "tag", Value: "g2"},
		{Entity: "6", Attribute: "tag", Value: "g2"},
		// Bridge
		{Entity: "100", Attribute: "tag", Value: "g1"},
		{Entity: "100", Attribute: "tag", Value: "g2"},
	}
	snap := factstore.BuildSnapshotFromTriples(triples, 1, factstore.SnapshotOptions{})

	e := &Executor{
		Registry:      mlruntime.NewRegistry(),
		GraphProvider: &stubGraphProvider{snap: snap},
	}

	plan := &planner.QueryPlan{
		BlockName: "Test related",
		Steps: []planner.PlanStep{
			&planner.GraphSnapshot{CacheKey: "test", Options: map[string]any{}, Into: "graph"},
			&planner.MLComputation{
				Function: planner.FuncPPRTopK,
				Input:    "",
				Params: map[string]any{
					"graph_var":  "graph",
					"seeds_expr": []ast.Expr{&ast.LiteralExpr{Value: 1.0}},
					"top_k":      3,
					"damping":    0.85,
				},
				Into: "related_records",
			},
		},
	}

	result, err := e.Run(context.Background(), plan)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	ml, ok := result.Vars["related_records"].(map[string]any)
	if !ok {
		t.Fatalf("expected related_records output map, got %T", result.Vars["related_records"])
	}
	results, ok := ml["results"].([]mlruntime.Result)
	if !ok {
		t.Fatalf("expected mlruntime.Result slice, got %T", ml["results"])
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}

	// The bridge node 100 is connected to both cliques and thus naturally
	// has the highest PPR mass. Below it, only members of seed-clique A
	// (entities 2 and 3) should appear in the top 3; never 4–6 from
	// clique B, which sit two hops away through the bridge.
	for i, r := range results {
		ent := r.Explanation.Inputs["entity"].(string)
		switch ent {
		case "4", "5", "6":
			t.Errorf("rank %d: clique-B node %q must not appear in top 3 (results=%+v)", i+1, ent, results)
		}
	}
}

func TestExecutorRelatedSeedsResolveFromLiterals(t *testing.T) {
	triples := []factstore.FactTriple{
		{Entity: "A", Attribute: "tag", Value: "x"},
		{Entity: "B", Attribute: "tag", Value: "x"},
		{Entity: "C", Attribute: "tag", Value: "y"},
	}
	snap := factstore.BuildSnapshotFromTriples(triples, 1, factstore.SnapshotOptions{})

	e := &Executor{
		Registry:      mlruntime.NewRegistry(),
		GraphProvider: &stubGraphProvider{snap: snap},
	}

	plan := &planner.QueryPlan{
		BlockName: "string seeds",
		Steps: []planner.PlanStep{
			&planner.GraphSnapshot{CacheKey: "test", Options: map[string]any{}, Into: "graph"},
			&planner.MLComputation{
				Function: planner.FuncPPRTopK,
				Input:    "",
				Params: map[string]any{
					"graph_var":  "graph",
					"seeds_expr": []ast.Expr{&ast.LiteralExpr{Value: "A"}},
					"top_k":      5,
				},
				Into: "related_records",
			},
		},
	}

	_, err := e.Run(context.Background(), plan)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestExtractFlaggedIDsHandlesPPRFloats(t *testing.T) {
	out := map[string]any{
		"results": []mlruntime.Result{
			{EntityID: 1, Value: 0.5},
			{EntityID: 2, Value: 0.0}, // zero score → not flagged
			{EntityID: 3, Value: 0.1},
		},
	}
	ids, ok := extractFlaggedIDs(out)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !ids[1] || !ids[3] {
		t.Errorf("expected IDs 1 and 3 flagged, got %v", ids)
	}
	if ids[2] {
		t.Errorf("ID 2 had score 0; should not be flagged")
	}
}

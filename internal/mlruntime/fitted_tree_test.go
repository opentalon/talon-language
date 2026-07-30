package mlruntime

import (
	"context"
	"testing"
)

// TestFittedTreeWalk: the predictor walks a pre-fitted tree (no training) and
// classifies each candidate down the correct path.
func TestFittedTreeWalk(t *testing.T) {
	nodes := []FittedTreeNode{
		{Index: 0, Feature: "km", Threshold: 30000, Left: 1, Right: 2},
		{Index: 1, Leaf: true, Class: "low", Purity: 1.0},
		{Index: 2, Feature: "age", Threshold: 5, Left: 3, Right: 4},
		{Index: 3, Leaf: true, Class: "low", Purity: 0.8},
		{Index: 4, Leaf: true, Class: "high", Purity: 1.0},
	}
	features := []string{"km", "age"}
	in := Input{
		Rows: [][]any{{1}, {2}, {3}},
		Entities: map[int]map[string]any{
			1: {"km": 50000.0, "age": 8.0}, // km>30k, age>5 → node4 high
			2: {"km": 10000.0, "age": 2.0}, // km<=30k    → node1 low
			3: {"km": 50000.0, "age": 4.0}, // km>30k, age<=5 → node3 low(0.8)
		},
		Params: map[string]any{
			"feature_names": features,
			"fitted_tree":   nodes,
		},
	}
	results, err := (&DecisionTreePredictor{}).Compute(context.Background(), in)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	got := map[int]Result{}
	for _, r := range results {
		got[r.EntityID] = r
	}
	if got[1].Value != "high" || got[1].Explanation.Confidence != 1.0 {
		t.Errorf("entity 1: got %v (conf %v), want high/1.0", got[1].Value, got[1].Explanation.Confidence)
	}
	if got[2].Value != "low" || got[2].Explanation.Confidence != 1.0 {
		t.Errorf("entity 2: got %v (conf %v), want low/1.0", got[2].Value, got[2].Explanation.Confidence)
	}
	if got[3].Value != "low" || got[3].Explanation.Confidence != 0.8 {
		t.Errorf("entity 3: got %v (conf %v), want low/0.8", got[3].Value, got[3].Explanation.Confidence)
	}
}

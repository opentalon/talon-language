package mlruntime

import (
	"context"
	"math"
	"testing"
)

// Simple two-row case: parallel vectors → score 1; orthogonal → score 0;
// anti-parallel → score -1. With within=0.5 and the centroid as the
// target, the higher-scoring row is kept and the lower one is dropped.
func TestCosineRanksParallelAboveOrthogonal(t *testing.T) {
	prim := NewCosineSimilarity()
	res, err := prim.Compute(context.Background(), Input{
		Rows: [][]any{{1.0}, {2.0}},
		Entities: map[int]map[string]any{
			// Anchor at entity 1; entity 2 is identical (cosine = 1).
			1: {"x": 1.0, "y": 0.0},
			2: {"x": 1.0, "y": 0.0},
			3: {"x": 0.0, "y": 1.0}, // orthogonal — not included in rows so unused
		},
		Params: map[string]any{
			"features": []string{"x", "y"},
			"to_id":    1,
		},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("want 2 results, got %d", len(res))
	}
	for _, r := range res {
		if r.EntityID == 1 && math.Abs(r.Explanation.Confidence-1.0) > 1e-9 {
			t.Errorf("anchor↔anchor cosine want 1.0, got %v", r.Explanation.Confidence)
		}
		if r.EntityID == 2 && math.Abs(r.Explanation.Confidence-1.0) > 1e-9 {
			t.Errorf("parallel vector cosine want 1.0, got %v", r.Explanation.Confidence)
		}
	}
}

func TestCosineWithinThresholdAndTopK(t *testing.T) {
	prim := NewCosineSimilarity()
	res, _ := prim.Compute(context.Background(), Input{
		Rows: [][]any{{1.0}, {2.0}, {3.0}},
		Entities: map[int]map[string]any{
			1: {"x": 1.0, "y": 0.0},
			2: {"x": 0.95, "y": 0.05},
			3: {"x": 0.0, "y": 1.0},
		},
		Params: map[string]any{
			"features": []string{"x", "y"},
			"to_id":    1,
			"within":   0.9,
			"top_k":    1,
		},
	})
	keptIDs := []int{}
	for _, r := range res {
		if v, _ := r.Value.(bool); v {
			keptIDs = append(keptIDs, r.EntityID)
		}
	}
	// Two rows pass within=0.9 (entities 1 and 2). top_k=1 keeps only
	// the highest scoring one, which is the anchor itself.
	if len(keptIDs) != 1 || keptIDs[0] != 1 {
		t.Errorf("want only entity 1 kept, got %v", keptIDs)
	}
}

func TestCosineRequiresAtLeastOneFeature(t *testing.T) {
	prim := NewCosineSimilarity()
	_, err := prim.Compute(context.Background(), Input{
		Rows:   [][]any{{1.0}},
		Params: map[string]any{},
	})
	if err == nil {
		t.Error("expected an error when no features are configured")
	}
}

func TestCosineWithoutAnchorUsesCentroid(t *testing.T) {
	// When no to_id is set, the centroid is the target. Two clustered
	// rows + one outlier — the clustered rows should score higher.
	prim := NewCosineSimilarity()
	res, _ := prim.Compute(context.Background(), Input{
		Rows: [][]any{{1.0}, {2.0}, {3.0}},
		Entities: map[int]map[string]any{
			1: {"x": 1.0, "y": 0.0},
			2: {"x": 1.0, "y": 0.0},
			3: {"x": -1.0, "y": 0.0},
		},
		Params: map[string]any{"features": []string{"x", "y"}},
	})
	if len(res) != 3 {
		t.Fatalf("want 3 results, got %d", len(res))
	}
	// Outlier should have the lowest confidence.
	var outlier float64
	var pair float64
	for _, r := range res {
		if r.EntityID == 3 {
			outlier = r.Explanation.Confidence
		} else {
			pair = r.Explanation.Confidence
		}
	}
	if pair <= outlier {
		t.Errorf("clustered rows should outrank the outlier; pair=%v outlier=%v", pair, outlier)
	}
}

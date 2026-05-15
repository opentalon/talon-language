package mlruntime

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestLearnedThresholdP95FlagsTopRows(t *testing.T) {
	rows := [][]any{
		{1, 10.0}, {2, 20.0}, {3, 30.0}, {4, 40.0}, {5, 50.0},
		{6, 60.0}, {7, 70.0}, {8, 80.0}, {9, 90.0}, {10, 100.0},
	}
	res, err := (&LearnedThreshold{}).Compute(context.Background(), Input{
		Rows:   rows,
		Params: map[string]any{"method": "p95", "op": ">"},
	})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if len(res) != len(rows) {
		t.Fatalf("want %d results, got %d", len(rows), len(res))
	}
	// percentile(p95) over 10 sorted values = 95
	flagged := map[int]bool{}
	for _, r := range res {
		if v, _ := r.Value.(bool); v {
			flagged[r.EntityID] = true
		}
		if r.Explanation.Threshold == nil || math.Abs(r.Explanation.Threshold.Value-95.5) > 1 {
			t.Fatalf("entity %d: threshold value %v not near 95", r.EntityID, r.Explanation.Threshold)
		}
	}
	if !flagged[10] {
		t.Fatalf("entity 10 (value 100) should be flagged, got: %v", flagged)
	}
	if flagged[9] {
		t.Fatalf("entity 9 (value 90) should not be flagged, got: %v", flagged)
	}
}

func TestLearnedThresholdDefaultOpIsGreater(t *testing.T) {
	rows := [][]any{{1, 1.0}, {2, 2.0}, {3, 3.0}, {4, 10.0}}
	res, err := (&LearnedThreshold{}).Compute(context.Background(), Input{
		Rows:   rows,
		Params: map[string]any{"method": "p50"},
	})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	for _, r := range res {
		if r.EntityID == 4 {
			if v, _ := r.Value.(bool); !v {
				t.Fatalf("entity 4 (>p50) expected flagged, got false")
			}
		}
	}
}

func TestLearnedThresholdRejectsInvalidMethod(t *testing.T) {
	_, err := (&LearnedThreshold{}).Compute(context.Background(), Input{
		Rows:   [][]any{{1, 1.0}, {2, 2.0}, {3, 3.0}},
		Params: map[string]any{"method": "bogus"},
	})
	if !errors.Is(err, ErrInvalidMethod) {
		t.Fatalf("want ErrInvalidMethod, got %v", err)
	}
}

func TestLearnedThresholdRejectsOutOfRangePercentile(t *testing.T) {
	_, err := (&LearnedThreshold{}).Compute(context.Background(), Input{
		Rows:   [][]any{{1, 1.0}, {2, 2.0}, {3, 3.0}},
		Params: map[string]any{"method": "p150"},
	})
	if !errors.Is(err, ErrInvalidMethod) {
		t.Fatalf("want ErrInvalidMethod, got %v", err)
	}
}

func TestLearnedThresholdSampleBelowMinimum(t *testing.T) {
	_, err := (&LearnedThreshold{}).Compute(context.Background(), Input{
		Rows:   [][]any{{1, 1.0}, {2, 2.0}},
		Params: map[string]any{"method": "p95"},
	})
	if !errors.Is(err, ErrSampleTooSmall) {
		t.Fatalf("want ErrSampleTooSmall, got %v", err)
	}
}

func TestLearnedThresholdLessThanOp(t *testing.T) {
	rows := [][]any{{1, 1.0}, {2, 2.0}, {3, 3.0}, {4, 100.0}}
	res, err := (&LearnedThreshold{}).Compute(context.Background(), Input{
		Rows:   rows,
		Params: map[string]any{"method": "p50", "op": "<"},
	})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	flagged := map[int]bool{}
	for _, r := range res {
		if v, _ := r.Value.(bool); v {
			flagged[r.EntityID] = true
		}
	}
	if !flagged[1] || flagged[4] {
		t.Fatalf("expected entity 1 flagged, entity 4 not. got %v", flagged)
	}
}

func TestLearnedThresholdExplanationHasRule(t *testing.T) {
	rows := [][]any{{1, 1.0}, {2, 5.0}, {3, 10.0}}
	res, _ := (&LearnedThreshold{}).Compute(context.Background(), Input{
		Rows:   rows,
		Params: map[string]any{"method": "p90", "op": ">="},
	})
	for _, r := range res {
		if r.Explanation.Primitive != FuncLearnedThreshold {
			t.Fatalf("primitive name %q", r.Explanation.Primitive)
		}
		if len(r.Explanation.Rules) == 0 {
			t.Fatalf("entity %d: no rules", r.EntityID)
		}
		if r.Explanation.Rules[0].Op != ">=" {
			t.Fatalf("entity %d: rule op %q", r.EntityID, r.Explanation.Rules[0].Op)
		}
		if r.Explanation.Threshold.Method != "p90" {
			t.Fatalf("entity %d: threshold method %q", r.EntityID, r.Explanation.Threshold.Method)
		}
	}
}

func TestPercentileLinearInterpolation(t *testing.T) {
	xs := []float64{10, 20, 30, 40, 50}
	if got := percentile(xs, 50); math.Abs(got-30) > 1e-9 {
		t.Fatalf("p50 want 30, got %v", got)
	}
	if got := percentile(xs, 0); math.Abs(got-10) > 1e-9 {
		t.Fatalf("p0 want 10, got %v", got)
	}
	if got := percentile(xs, 100); math.Abs(got-50) > 1e-9 {
		t.Fatalf("p100 want 50, got %v", got)
	}
	if got := percentile(xs, 75); math.Abs(got-40) > 1e-9 {
		t.Fatalf("p75 want 40, got %v", got)
	}
}

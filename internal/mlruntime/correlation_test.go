package mlruntime

import (
	"context"
	"errors"
	"math"
	"testing"
)

// xyRows builds [entity_id, x, y] rows from parallel series.
func xyRows(xs, ys []float64) [][]any {
	rows := make([][]any, len(xs))
	for i := range xs {
		rows[i] = []any{i + 1, xs[i], ys[i]}
	}
	return rows
}

func computeCorr(t *testing.T, xs, ys []float64, op string, threshold float64) []Result {
	t.Helper()
	res, err := NewPearsonCorrelation().Compute(context.Background(), Input{
		Rows:   xyRows(xs, ys),
		Schema: map[string]int{"entity_id": 0, "value_x": 1, "value_y": 2},
		Params: map[string]any{"op": op, "threshold": threshold, "attr_x": "x", "attr_y": "y"},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	return res
}

func rOf(t *testing.T, res []Result) float64 {
	t.Helper()
	if len(res) == 0 {
		t.Fatal("no results")
	}
	r, ok := res[0].Explanation.Inputs["r"].(float64)
	if !ok {
		t.Fatalf("no r in explanation: %+v", res[0].Explanation.Inputs)
	}
	return r
}

func TestPearsonPerfectPositive(t *testing.T) {
	// y = 2x → r = 1.0.
	res := computeCorr(t, []float64{1, 2, 3, 4, 5}, []float64{2, 4, 6, 8, 10}, ">", 0.7)
	if r := rOf(t, res); math.Abs(r-1.0) > 1e-9 {
		t.Fatalf("r = %v, want 1.0", r)
	}
	for _, x := range res {
		if x.Value != true {
			t.Fatalf("entity %d: r=1.0 > 0.7 should flag true, got %v", x.EntityID, x.Value)
		}
	}
}

func TestPearsonPerfectNegative(t *testing.T) {
	// y = -x → r = -1.0.
	res := computeCorr(t, []float64{1, 2, 3, 4, 5}, []float64{-1, -2, -3, -4, -5}, "<", -0.7)
	if r := rOf(t, res); math.Abs(r+1.0) > 1e-9 {
		t.Fatalf("r = %v, want -1.0", r)
	}
	if res[0].Value != true {
		t.Fatalf("r=-1.0 < -0.7 should flag true, got %v", res[0].Value)
	}
}

func TestPearsonUncorrelated(t *testing.T) {
	// Symmetric cloud → r = 0 exactly.
	res := computeCorr(t, []float64{1, 1, -1, -1, 0}, []float64{1, -1, 1, -1, 0}, ">", 0.5)
	if r := rOf(t, res); math.Abs(r) > 1e-9 {
		t.Fatalf("r = %v, want 0", r)
	}
	if res[0].Value != false {
		t.Fatalf("r=0 > 0.5 should flag false, got %v", res[0].Value)
	}
}

func TestPearsonZeroVarianceIsZero(t *testing.T) {
	// x constant → r undefined → treated as 0.
	res := computeCorr(t, []float64{5, 5, 5, 5}, []float64{1, 2, 3, 4}, ">", 0.5)
	if r := rOf(t, res); r != 0 {
		t.Fatalf("r = %v, want 0 for zero-variance series", r)
	}
}

func TestPearsonSampleTooSmall(t *testing.T) {
	_, err := NewPearsonCorrelation().Compute(context.Background(), Input{
		Rows:   xyRows([]float64{1, 2}, []float64{2, 4}),
		Schema: map[string]int{"entity_id": 0, "value_x": 1, "value_y": 2},
		Params: map[string]any{"op": ">", "threshold": 0.5},
	})
	if !errors.Is(err, ErrSampleTooSmall) {
		t.Fatalf("err = %v, want ErrSampleTooSmall", err)
	}
}

func TestPearsonExplanationEvidence(t *testing.T) {
	res := computeCorr(t, []float64{1, 2, 3, 4, 5}, []float64{2, 4, 6, 8, 10}, ">", 0.7)
	ex := res[0].Explanation
	if ex.Primitive != FuncCorrelationPearson {
		t.Errorf("primitive = %q", ex.Primitive)
	}
	if ex.Threshold == nil || ex.Threshold.Sample != 5 || ex.Threshold.Method != "pearson" {
		t.Errorf("threshold evidence = %+v", ex.Threshold)
	}
	if len(ex.Rules) != 1 || ex.Rules[0].Op != ">" {
		t.Errorf("rules = %+v", ex.Rules)
	}
}

package mlruntime

import (
	"context"
	"testing"
)

// Strict linearly-decaying series should produce a deterministic
// projection. With y_t = 100 - t and threshold 0, the value hits zero
// at t = 100. Holt's method on a clean linear series fits trend = -1
// and level = 90 (the last point), so days_until from there is 90.
func TestForecastLinearDecay(t *testing.T) {
	prim := NewExpSmoothingForecast()
	series := []float64{100, 99, 98, 97, 96, 95, 94, 93, 92, 91, 90}
	res, err := prim.Compute(context.Background(), Input{
		Rows: [][]any{{1.0}},
		Params: map[string]any{
			"series":    series,
			"threshold": 0.0,
			"predicate": "<=",
			"alpha":     1.0, // pure last-observation level
			"beta":      1.0, // pure last-step trend
		},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 result, got %d", len(res))
	}
	days := res[0].Value.(float64)
	// With α=β=1: level=90, trend=-1, projected=90+d*(-1) ≤ 0 → d=90.
	if days != 90 {
		t.Errorf("want days_until=90 for linear decay, got %v", days)
	}
}

// Threshold already satisfied at t=0: the projection reports 0 days.
func TestForecastAlreadyCrossed(t *testing.T) {
	prim := NewExpSmoothingForecast()
	res, _ := prim.Compute(context.Background(), Input{
		Rows: [][]any{{1.0}},
		Params: map[string]any{
			"series":    []float64{5, 4, 3, 2, 1, -1},
			"threshold": 0.0,
			"predicate": "<=",
			"alpha":     1.0,
			"beta":      1.0,
		},
	})
	if res[0].Value.(float64) != 0 {
		t.Errorf("want days_until=0 (already crossed), got %v", res[0].Value)
	}
}

// A flat series never crosses a non-zero threshold; the result is
// capped at max_steps with confidence 0 so callers can filter.
func TestForecastNeverCrossesCappedAtMaxSteps(t *testing.T) {
	prim := NewExpSmoothingForecast()
	res, _ := prim.Compute(context.Background(), Input{
		Rows: [][]any{{1.0}},
		Params: map[string]any{
			"series":    []float64{50, 50, 50, 50, 50},
			"threshold": 0.0,
			"predicate": "<=",
			"max_steps": 10,
			"alpha":     1.0,
			"beta":      1.0,
		},
	})
	if res[0].Value.(float64) != 10 {
		t.Errorf("want days_until=10 (cap), got %v", res[0].Value)
	}
	if res[0].Explanation.Confidence != 0 {
		t.Errorf("want confidence 0 on a never-crossing forecast, got %v", res[0].Explanation.Confidence)
	}
}

// series_by_id lets the caller provide a different series per entity —
// the realistic shape when the planner pulls historical attr values
// from the FactStore per row.
func TestForecastSeriesByID(t *testing.T) {
	prim := NewExpSmoothingForecast()
	res, _ := prim.Compute(context.Background(), Input{
		Rows: [][]any{{1.0}, {2.0}},
		Params: map[string]any{
			"series_by_id": map[int][]float64{
				1: {10, 9, 8, 7, 6, 5}, // hits 0 at t=5 from level=5, trend=-1 → days=5
				2: {20, 19, 18, 17, 16, 15},
			},
			"threshold": 0.0,
			"predicate": "<=",
			"alpha":     1.0,
			"beta":      1.0,
		},
	})
	byID := map[int]float64{}
	for _, r := range res {
		byID[r.EntityID] = r.Value.(float64)
	}
	if byID[1] != 5 || byID[2] != 15 {
		t.Errorf("want [1:5, 2:15], got %v", byID)
	}
}

// Forecast blocks without a `predict value <op> N` clause should fall
// back to the stock-out default (predicate <= 0). Reproduces the CI
// failure where `forecast "Parts stock-out" { ... }` (no predict
// clause) errored at runtime asking for an explicit threshold.
func TestForecastDefaultsToStockOut(t *testing.T) {
	prim := NewExpSmoothingForecast()
	res, err := prim.Compute(context.Background(), Input{
		Rows: [][]any{{1.0}},
		Params: map[string]any{
			// No "predicate" / "threshold" — primitive must default to
			// `<= 0`.
			"series": []float64{10, 9, 8, 7, 6, 5},
			"alpha":  1.0,
			"beta":   1.0,
		},
	})
	if err != nil {
		t.Fatalf("Compute should not error with default predicate: %v", err)
	}
	// Level=5, trend=-1, threshold=0 ⇒ d such that 5 + d*(-1) <= 0 ⇒ d = 5.
	if res[0].Value.(float64) != 5 {
		t.Errorf("default <= 0 stock-out: want days=5, got %v", res[0].Value)
	}
}

// Series too short to fit a trend → placeholder result with confidence 0.
func TestForecastTooShortSeries(t *testing.T) {
	prim := NewExpSmoothingForecast()
	res, _ := prim.Compute(context.Background(), Input{
		Rows: [][]any{{1.0}},
		Params: map[string]any{
			"series":    []float64{42},
			"threshold": 0.0,
			"max_steps": 7,
		},
	})
	if res[0].Explanation.Confidence != 0 {
		t.Errorf("want confidence 0 on short series, got %v", res[0].Explanation.Confidence)
	}
}

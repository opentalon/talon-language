package mlruntime

import (
	"context"
	"math"
	"testing"
)

func wmaOf(t *testing.T, series []float64) float64 {
	t.Helper()
	res, err := NewWeightedMovingAverage().Compute(context.Background(), Input{
		Rows:   [][]any{{1}},
		Params: map[string]any{"series": series},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("got %d results, want 1", len(res))
	}
	v, ok := res[0].Value.(float64)
	if !ok {
		t.Fatalf("Value is %T, want float64", res[0].Value)
	}
	return v
}

func flatAvg(xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

// TestWMARecentDaysWeighMore — the issue's golden case: a 7-day series where
// the last 3 days spike should produce a WMA visibly above the flat average.
func TestWMARecentDaysWeighMore(t *testing.T) {
	series := []float64{10, 10, 10, 10, 50, 50, 50} // newest last
	wma := wmaOf(t, series)
	avg := flatAvg(series)
	if !(wma > avg) {
		t.Fatalf("wma %.3f should exceed flat avg %.3f when recent days spike", wma, avg)
	}
	// Exact linear-weighted value: Σ(i·xᵢ)/Σi = 1000/28.
	if want := 1000.0 / 28.0; math.Abs(wma-want) > 1e-9 {
		t.Fatalf("wma = %.6f, want %.6f", wma, want)
	}
}

// TestWMAOlderSpikeWeighsLess — mirror image: the same spike at the START of
// the window pulls the WMA *below* the flat average.
func TestWMAOlderSpikeWeighsLess(t *testing.T) {
	series := []float64{50, 50, 50, 10, 10, 10, 10} // spike is oldest
	if wma, avg := wmaOf(t, series), flatAvg(series); !(wma < avg) {
		t.Fatalf("wma %.3f should be below flat avg %.3f when the spike is old", wma, avg)
	}
}

// TestWMASinglePoint returns that point.
func TestWMASinglePoint(t *testing.T) {
	if got := wmaOf(t, []float64{42}); got != 42 {
		t.Fatalf("single-point wma = %v, want 42", got)
	}
}

// TestWMAEmptySeriesNoResult — an entity with no series yields no result.
func TestWMAEmptySeries(t *testing.T) {
	res, err := NewWeightedMovingAverage().Compute(context.Background(), Input{
		Rows:   [][]any{{1}},
		Params: map[string]any{"series": []float64{}},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("got %d results, want 0 for empty series", len(res))
	}
}

// TestWMAPerEntitySeries routes each entity to its own series via series_by_id.
func TestWMAPerEntitySeries(t *testing.T) {
	res, err := NewWeightedMovingAverage().Compute(context.Background(), Input{
		Rows: [][]any{{1}, {2}},
		Params: map[string]any{"series_by_id": map[int][]float64{
			1: {1, 1, 1},
			2: {0, 0, 6}, // newest weighs 3 → 18/6 = 3
		}},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	byID := map[int]float64{}
	for _, r := range res {
		byID[r.EntityID] = r.Value.(float64)
	}
	if byID[1] != 1 {
		t.Errorf("entity 1 wma = %v, want 1", byID[1])
	}
	if math.Abs(byID[2]-3) > 1e-9 {
		t.Errorf("entity 2 wma = %v, want 3", byID[2])
	}
}

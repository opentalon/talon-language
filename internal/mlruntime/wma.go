package mlruntime

import (
	"context"
)

// FuncWMA is the planner function name this primitive binds to. Must match
// planner.FuncWMA — duplicated as a string to avoid an import cycle.
const FuncWMA = "weighted_moving_average"

// WeightedMovingAverage reduces a numeric series to a single linearly-weighted
// average where recent observations weigh more. For a chronological series
// x₁…xₙ (oldest first, newest last) the weight of xᵢ is its position i, so the
// newest value carries weight n and the oldest weight 1:
//
//	wma = (1·x₁ + 2·x₂ + … + n·xₙ) / (1 + 2 + … + n)
//
// It backs the `calculate … weighted_moving_average` clause: the "current
// rate" case, smoothing recent history into one number without extrapolating
// (that is forecast's job). Pure arithmetic, deterministic, explainable.
//
// The per-entity series arrives via Params["series_by_id"] (map[int][]float64)
// or a shared Params["series"] ([]float64) — the same shape the forecast
// primitive consumes. Building that series from the FactStore's time
// dimension is a shared follow-up (see forecast); the primitive itself is
// exercised directly.
type WeightedMovingAverage struct{}

// NewWeightedMovingAverage constructs the primitive.
func NewWeightedMovingAverage() *WeightedMovingAverage { return &WeightedMovingAverage{} }

// Name reports the planner function name this primitive serves.
func (w *WeightedMovingAverage) Name() string { return FuncWMA }

// Compute returns one Result per input row, its Value the linearly-weighted
// average of that entity's series (a float64 scalar, not a filter verdict).
func (w *WeightedMovingAverage) Compute(_ context.Context, in Input) ([]Result, error) {
	seriesByID, _ := in.Params["series_by_id"].(map[int][]float64)
	defaultSeries, _ := in.Params["series"].([]float64)

	results := make([]Result, 0, len(in.Rows))
	for _, row := range in.Rows {
		if len(row) == 0 {
			continue
		}
		id, ok := toInt(row[0])
		if !ok {
			continue
		}
		series := seriesByID[id]
		if len(series) == 0 {
			series = defaultSeries
		}
		if len(series) == 0 {
			continue
		}
		wma := LinearWMA(series)
		results = append(results, Result{
			EntityID: id,
			Value:    wma,
			Explanation: Explanation{
				Primitive: FuncWMA,
				EntityID:  id,
				Inputs: map[string]any{
					"wma":    wma,
					"n":      len(series),
					"newest": series[len(series)-1],
				},
			},
		})
	}
	return results, nil
}

// LinearWMA computes the linearly-weighted average of a chronological series
// (newest last), giving the newest value the highest weight. Returns 0 for an
// empty series.
func LinearWMA(xs []float64) float64 {
	var num, den float64
	for i, x := range xs {
		w := float64(i + 1)
		num += w * x
		den += w
	}
	if den == 0 {
		return 0
	}
	return num / den
}

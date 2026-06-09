package mlruntime

import (
	"context"
)

// ExpSmoothingForecast satisfies the ForecastExpSmoothing primitive.
// For each candidate entity, it fits Holt's linear-trend exponential
// smoothing on a time series and projects forward until a predicate
// fires (e.g. "value <= 0" — the stock-out date).
//
// Holt's method maintains two state variables per period:
//
//	level_t = α · y_t        + (1-α) · (level_{t-1} + trend_{t-1})
//	trend_t = β · (level_t - level_{t-1}) + (1-β) · trend_{t-1}
//
// And projects k steps ahead as level_T + k · trend_T.
//
// Params:
//
//	series       []float64 — the per-row time series. The planner is
//	                         responsible for fetching it; the primitive
//	                         doesn't talk to a FactStore directly. Each
//	                         entity's series is keyed by ID in
//	                         params["series_by_id"] (preferred) or, for
//	                         single-series cases, by the top-level "series"
//	                         key.
//	series_by_id map[int][]float64 — per-entity series (preferred shape).
//	predicate    string    — comparison operator. One of "<", "<=", ">",
//	                         ">=", "==". Default "<=".
//	threshold    float64   — the value the projection must cross. The
//	                         result is the number of steps (days) until
//	                         the projected value satisfies predicate.
//	alpha        float64   — level smoothing factor in (0, 1]. Default 0.3.
//	beta         float64   — trend smoothing factor in (0, 1]. Default 0.1.
//	max_steps    int       — safety cap on the forecast horizon. Default
//	                         365. A row whose projection never crosses
//	                         the threshold within max_steps returns
//	                         Value=max_steps with confidence 0.
//
// Result Value is an int (days_until), so downstream `when days_until <
// 7` filters work naturally against the rendered output. The
// explanation records the fitted level + trend so reviewers can see the
// model behind the projection.
type ExpSmoothingForecast struct{}

// NewExpSmoothingForecast constructs the primitive.
func NewExpSmoothingForecast() *ExpSmoothingForecast { return &ExpSmoothingForecast{} }

// Name returns the planner function constant this primitive serves.
func (*ExpSmoothingForecast) Name() string { return "forecast_exponential_smoothing" }

// Compute projects each row's series forward and reports days_until
// the predicate fires.
func (f *ExpSmoothingForecast) Compute(_ context.Context, in Input) ([]Result, error) {
	alpha := readFloatOr(in.Params, "alpha", 0.3)
	beta := readFloatOr(in.Params, "beta", 0.1)
	maxSteps := readIntOr(in.Params, "max_steps", 365)
	predicate := readString(in.Params, "predicate")
	if predicate == "" {
		// Default to the stock-out interpretation: how many days until
		// the value reaches zero. Matches every example forecast block
		// today (`forecast "Stock-out" { series attr "x" ... }`).
		predicate = "<="
	}
	threshold, hasThreshold := readFloat(in.Params, "threshold")
	if !hasThreshold {
		threshold = 0
		hasThreshold = true
	}

	// Two shapes for the series data: map[int][]float64 (per-entity) or
	// []float64 (one series shared across all rows — useful for testing).
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
		if len(series) < 2 {
			// Need at least two points to fit a trend. Return a
			// placeholder result so downstream rendering still produces
			// something readable, with confidence 0 to mark it weak.
			results = append(results, Result{
				EntityID: id,
				Value:    float64(maxSteps),
				Explanation: Explanation{
					Primitive:  "forecast_exponential_smoothing",
					EntityID:   id,
					Confidence: 0,
					Inputs:     map[string]any{"reason": "series too short"},
				},
			})
			continue
		}

		level, trend := fitHolt(series, alpha, beta)
		days := 0
		projected := level
		// 0 days is the *current* state; step forward until the
		// predicate fires or we hit the safety cap.
		if hasThreshold && evalForecastPredicate(predicate, projected, threshold) {
			results = append(results, Result{
				EntityID: id,
				Value:    float64(0),
				Explanation: Explanation{
					Primitive:  "forecast_exponential_smoothing",
					EntityID:   id,
					Confidence: 1,
					Inputs: map[string]any{
						"level":     level,
						"trend":     trend,
						"days":      0,
						"predicate": predicate,
						"threshold": threshold,
					},
				},
			})
			continue
		}
		for days < maxSteps {
			days++
			projected = level + float64(days)*trend
			if hasThreshold && evalForecastPredicate(predicate, projected, threshold) {
				break
			}
		}
		confidence := 1.0
		if days >= maxSteps {
			confidence = 0 // never crossed — report the cap, low confidence
		}
		results = append(results, Result{
			EntityID: id,
			Value:    float64(days),
			Explanation: Explanation{
				Primitive:  "forecast_exponential_smoothing",
				EntityID:   id,
				Confidence: confidence,
				Inputs: map[string]any{
					"level":     level,
					"trend":     trend,
					"days":      days,
					"predicate": predicate,
					"threshold": threshold,
				},
			},
		})
	}
	_ = hasThreshold
	return results, nil
}

// fitHolt runs Holt's linear-trend exponential smoothing over the
// series and returns the final (level, trend). Initialisation uses the
// first two observations: level_1 = y_1, trend_1 = y_2 - y_1.
func fitHolt(series []float64, alpha, beta float64) (level, trend float64) {
	level = series[0]
	trend = series[1] - series[0]
	for i := 1; i < len(series); i++ {
		prevLevel := level
		level = alpha*series[i] + (1-alpha)*(prevLevel+trend)
		trend = beta*(level-prevLevel) + (1-beta)*trend
	}
	return level, trend
}

func evalForecastPredicate(op string, projected, threshold float64) bool {
	switch op {
	case "<":
		return projected < threshold
	case "<=":
		return projected <= threshold
	case ">":
		return projected > threshold
	case ">=":
		return projected >= threshold
	case "==":
		return projected == threshold
	}
	return false
}

func readFloatOr(params map[string]any, key string, fallback float64) float64 {
	if f, ok := readFloat(params, key); ok {
		return f
	}
	return fallback
}

func readIntOr(params map[string]any, key string, fallback int) int {
	if n, ok := readInt(params, key); ok {
		return n
	}
	return fallback
}

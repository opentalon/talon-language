package mlruntime

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// FuncAnomalyZscore is the planner function name this primitive binds to.
// Must match planner.FuncAnomalyZscore — duplicated as a string to avoid
// importing planner here.
const FuncAnomalyZscore = "anomaly_zscore"

// DefaultZThreshold flags samples whose |z| exceeds this value.
// 2.5 ≈ p99 under a normal distribution; tunable via Input.Params["threshold"].
const DefaultZThreshold = 2.5

// MinAnomalySample is the smallest sample size the z-score primitive accepts.
// Below this the mean/stddev estimate is too noisy to be useful.
const MinAnomalySample = 3

// ErrSampleTooSmall is returned when the input row count is below MinAnomalySample.
var ErrSampleTooSmall = errors.New("anomaly: sample smaller than minimum window")

// ZScoreAnomaly flags rows whose value column deviates from the sample
// mean by more than threshold standard deviations.
//
// Input row shape: [entity_id, value]. If Input.Schema names "entity_id"
// and "value", those indices are used instead. Non-numeric values are skipped
// when computing mean/stddev and produce no result row.
type ZScoreAnomaly struct{}

// NewZScoreAnomaly returns a fresh primitive instance. Stateless.
func NewZScoreAnomaly() *ZScoreAnomaly { return &ZScoreAnomaly{} }

// Name implements Primitive.
func (z *ZScoreAnomaly) Name() string { return FuncAnomalyZscore }

// Compute returns one Result per input row, with Value=true when the row is
// flagged anomalous. Explanation records the observed value, sample mean,
// stddev, and z so audit logs can reproduce the decision.
func (z *ZScoreAnomaly) Compute(_ context.Context, in Input) ([]Result, error) {
	if len(in.Rows) < MinAnomalySample {
		return nil, fmt.Errorf("%w: got %d, need %d", ErrSampleTooSmall, len(in.Rows), MinAnomalySample)
	}

	idIdx, valIdx := columnIndex(in.Schema, "entity_id", 0), columnIndex(in.Schema, "value", 1)

	values := make([]float64, 0, len(in.Rows))
	for _, row := range in.Rows {
		if v, ok := numericAt(row, valIdx); ok {
			values = append(values, v)
		}
	}
	if len(values) < MinAnomalySample {
		return nil, fmt.Errorf("%w: %d numeric values among %d rows", ErrSampleTooSmall, len(values), len(in.Rows))
	}

	mean, stddev := meanStddev(values)
	threshold := DefaultZThreshold
	if t, ok := numericParam(in.Params, "threshold"); ok {
		threshold = t
	}

	results := make([]Result, 0, len(in.Rows))
	for _, row := range in.Rows {
		val, ok := numericAt(row, valIdx)
		if !ok {
			continue
		}
		entityID, _ := intAt(row, idIdx)

		var z float64
		if stddev > 0 {
			z = (val - mean) / stddev
		}
		flagged := stddev > 0 && math.Abs(z) > threshold

		results = append(results, Result{
			EntityID: entityID,
			Value:    flagged,
			Explanation: Explanation{
				Primitive: FuncAnomalyZscore,
				EntityID:  entityID,
				Inputs: map[string]any{
					"observed": val,
					"mean":     mean,
					"stddev":   stddev,
					"z":        z,
				},
				Rules: []Rule{{
					Attr:     "z_score",
					Op:       ">",
					Value:    threshold,
					Observed: math.Abs(z),
				}},
				Threshold: &Threshold{
					Method: "mean_stddev",
					Value:  threshold,
					Sample: len(values),
				},
			},
		})
	}
	return results, nil
}

func meanStddev(xs []float64) (float64, float64) {
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(len(xs))
	var sqSum float64
	for _, x := range xs {
		d := x - mean
		sqSum += d * d
	}
	variance := sqSum / float64(len(xs))
	return mean, math.Sqrt(variance)
}

func columnIndex(schema map[string]int, name string, fallback int) int {
	if i, ok := schema[name]; ok {
		return i
	}
	return fallback
}

func numericAt(row []any, idx int) (float64, bool) {
	if idx < 0 || idx >= len(row) {
		return 0, false
	}
	switch v := row[idx].(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}

func intAt(row []any, idx int) (int, bool) {
	if idx < 0 || idx >= len(row) {
		return 0, false
	}
	switch v := row[idx].(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	}
	return 0, false
}

func numericParam(params map[string]any, key string) (float64, bool) {
	if params == nil {
		return 0, false
	}
	switch v := params[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}

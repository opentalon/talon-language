package mlruntime

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// FuncLearnedThreshold is the planner function name this primitive binds to.
const FuncLearnedThreshold = "learned_threshold"

// MinThresholdSample is the smallest sample size accepted before refusing to
// compute a learned threshold. Mirrors MinAnomalySample's rationale.
const MinThresholdSample = 3

// ErrInvalidMethod is returned when Params["method"] is not a recognised
// percentile spec.
var ErrInvalidMethod = errors.New("learned_threshold: invalid method")

// LearnedThreshold computes a percentile cut-off from the input column values
// and flags rows where the observed value relates to that cut-off via the
// configured operator. Op defaults to ">".
//
// Input row shape: [entity_id, value]. Params["method"] is required and must
// match `p<int>` (e.g. "p95"). Params["op"] is optional ("<", "<=", ">", ">=").
type LearnedThreshold struct{}

// NewLearnedThreshold returns a fresh primitive instance. Stateless.
func NewLearnedThreshold() *LearnedThreshold { return &LearnedThreshold{} }

// Name implements Primitive.
func (l *LearnedThreshold) Name() string { return FuncLearnedThreshold }

// Compute returns one Result per input row, with Value=true when the row
// passes the comparison against the learned threshold.
func (l *LearnedThreshold) Compute(_ context.Context, in Input) ([]Result, error) {
	method, _ := in.Params["method"].(string)
	pct, err := parsePercentile(method)
	if err != nil {
		return nil, err
	}

	op, _ := in.Params["op"].(string)
	if op == "" {
		op = ">"
	}

	if len(in.Rows) < MinThresholdSample {
		return nil, fmt.Errorf("%w: got %d, need %d", ErrSampleTooSmall, len(in.Rows), MinThresholdSample)
	}

	idIdx := columnIndex(in.Schema, "entity_id", 0)
	valIdx := columnIndex(in.Schema, "value", 1)

	values := make([]float64, 0, len(in.Rows))
	for _, row := range in.Rows {
		if v, ok := numericAt(row, valIdx); ok {
			values = append(values, v)
		}
	}
	if len(values) < MinThresholdSample {
		return nil, fmt.Errorf("%w: %d numeric values among %d rows", ErrSampleTooSmall, len(values), len(in.Rows))
	}

	threshold := percentile(values, pct)
	results := make([]Result, 0, len(in.Rows))
	for _, row := range in.Rows {
		val, ok := numericAt(row, valIdx)
		if !ok {
			continue
		}
		entityID, _ := intAt(row, idIdx)
		flagged := compareOp(op, val, threshold)
		results = append(results, Result{
			EntityID: entityID,
			Value:    flagged,
			Explanation: Explanation{
				Primitive: FuncLearnedThreshold,
				EntityID:  entityID,
				Inputs: map[string]any{
					"observed":  val,
					"percentile": pct,
				},
				Rules: []Rule{{
					Attr:     method,
					Op:       op,
					Value:    threshold,
					Observed: val,
				}},
				Threshold: &Threshold{
					Method: method,
					Value:  threshold,
					Sample: len(values),
				},
			},
		})
	}
	return results, nil
}

// parsePercentile accepts "p95", "P50", etc. and returns the numeric percentile.
func parsePercentile(method string) (float64, error) {
	if len(method) < 2 || (method[0] != 'p' && method[0] != 'P') {
		return 0, fmt.Errorf("%w: %q (expected p<int>)", ErrInvalidMethod, method)
	}
	n, err := strconv.Atoi(strings.TrimSpace(method[1:]))
	if err != nil || n < 0 || n > 100 {
		return 0, fmt.Errorf("%w: %q (percentile must be 0..100)", ErrInvalidMethod, method)
	}
	return float64(n), nil
}

// percentile returns the linear-interpolation percentile of xs.
// 0 ≤ pct ≤ 100; xs is copied + sorted internally.
func percentile(xs []float64, pct float64) float64 {
	sorted := append([]float64(nil), xs...)
	sort.Float64s(sorted)
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := pct / 100.0 * float64(len(sorted)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (rank-float64(lo))*(sorted[hi]-sorted[lo])
}

func compareOp(op string, left, right float64) bool {
	switch op {
	case ">":
		return left > right
	case ">=":
		return left >= right
	case "<":
		return left < right
	case "<=":
		return left <= right
	case "==", "=":
		return left == right
	case "!=", "not=":
		return left != right
	}
	return false
}

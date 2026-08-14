package executor

import (
	"context"
	"fmt"

	"github.com/opentalon/tln-language/internal/optimize"
	"github.com/opentalon/tln-language/internal/planner"
)

// execOptimizeACO runs Ant Colony Optimization over the candidate rows. The
// pairwise distance matrix is computed from the two coordinate attrs the
// planner threaded into Params. The result is the best tour (a permutation
// of entity IDs) plus the convergence history for explainability.
func (e *Executor) execOptimizeACO(_ context.Context, gc *planner.GoComputation, vars map[string]any) (any, error) {
	rows, _ := vars[gc.Input].([][]any)
	if len(rows) == 0 {
		return map[string]any{
			"function": gc.Function,
			"tour":     []int{},
			"length":   0.0,
		}, nil
	}

	xName, _ := gc.Params["x_attr"].(string)
	yName, _ := gc.Params["y_attr"].(string)
	attrIdx, _ := gc.Params["attr_indices"].(map[string]int)
	seed, _ := gc.Params["seed"].(int64)

	xIdx, ok1 := attrIdx[xName]
	yIdx, ok2 := attrIdx[yName]
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("optimize_aco: coordinate attrs %q/%q not in row index", xName, yName)
	}

	xs := make([]float64, len(rows))
	ys := make([]float64, len(rows))
	entityIDs := make([]int, len(rows))
	for i, row := range rows {
		if len(row) == 0 {
			continue
		}
		entityIDs[i], _ = toInt(row[0])
		if xIdx < len(row) {
			xs[i], _ = toFloat(row[xIdx])
		}
		if yIdx < len(row) {
			ys[i], _ = toFloat(row[yIdx])
		}
	}

	dist := optimize.EuclideanDistanceMatrix(xs, ys)
	res := optimize.ACO(dist, optimize.ACOConfig{Seed: seed})

	orderedIDs := make([]int, len(res.Tour))
	for i, idx := range res.Tour {
		orderedIDs[i] = entityIDs[idx]
	}

	return map[string]any{
		"function":   gc.Function,
		"tour":       orderedIDs,
		"length":     res.Length,
		"iterations": len(res.History),
		"history":    res.History,
	}, nil
}

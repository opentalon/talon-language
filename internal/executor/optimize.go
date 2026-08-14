package executor

import (
	"context"
	"fmt"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/optimize"
	"github.com/opentalon/tln-language/internal/planner"
)

// execOptimizePareto runs NSGA-II Pareto ranking over the candidate rows
// produced by the upstream Datalevin query. Each row's per-objective value
// is read from the column index the planner recorded; rows missing or with
// non-numeric values in any objective slot are skipped.
//
// The output is a structured map shaped so downstream steps (render_template,
// trace) and the explain pipeline can consume it without special-casing:
//
//	{
//	  "function":   "optimize_pareto",
//	  "frontier":   [ {entity_id, rank, crowding, dominated, values, row}, ... ],
//	  "all":        [ ... same shape, every individual ... ],
//	  "objectives": [ {name, dir}, ... ],
//	}
func (e *Executor) execOptimizePareto(_ context.Context, gc *planner.GoComputation, vars map[string]any) (any, error) {
	rows, _ := vars[gc.Input].([][]any)

	objClauses, ok := gc.Params["objectives"].([]ast.OptimizeClause)
	if !ok {
		return nil, fmt.Errorf("optimize_pareto: missing objectives param")
	}
	indices, _ := gc.Params["objective_value_indices"].([]int)
	if len(indices) != len(objClauses) {
		return nil, fmt.Errorf("optimize_pareto: objective_value_indices arity mismatch (%d vs %d)",
			len(indices), len(objClauses))
	}

	objs := make([]optimize.Objective, len(objClauses))
	for i, oc := range objClauses {
		name, _ := combineObjectiveAttrName(oc.Attr)
		objs[i] = optimize.Objective{Name: name, Dir: directionFor(oc.Direction)}
	}

	individuals := make([]optimize.Individual, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		entID, ok := toInt(row[0])
		if !ok {
			continue
		}
		values := make([]float64, 0, len(indices))
		skip := false
		for _, idx := range indices {
			if idx < 0 || idx >= len(row) {
				skip = true
				break
			}
			v, ok := toFloat(row[idx])
			if !ok {
				skip = true
				break
			}
			values = append(values, v)
		}
		if skip {
			continue
		}
		individuals = append(individuals, optimize.Individual{
			EntityID: entID,
			Values:   values,
			Row:      row,
		})
	}

	res, err := optimize.Pareto(individuals, objs)
	if err != nil {
		return nil, fmt.Errorf("optimize_pareto: %w", err)
	}

	return map[string]any{
		"function":   gc.Function,
		"frontier":   solutionsToMaps(res.Frontier, objs),
		"all":        solutionsToMaps(res.All, objs),
		"objectives": objectivesToMaps(objs),
	}, nil
}

func solutionsToMaps(sols []optimize.Solution, objs []optimize.Objective) []map[string]any {
	out := make([]map[string]any, 0, len(sols))
	for _, s := range sols {
		values := map[string]any{}
		for i, obj := range objs {
			values[obj.Name] = s.Values[i]
		}
		out = append(out, map[string]any{
			"entity_id": s.EntityID,
			"rank":      s.Rank,
			"crowding":  s.CrowdingDist,
			"dominated": s.DominatedCount,
			"dominates": s.Dominates,
			"values":    values,
			"row":       s.Row,
		})
	}
	return out
}

func objectivesToMaps(objs []optimize.Objective) []map[string]any {
	out := make([]map[string]any, 0, len(objs))
	for _, o := range objs {
		out = append(out, map[string]any{"name": o.Name, "direction": o.Dir.String()})
	}
	return out
}

func directionFor(s string) optimize.Direction {
	if s == "maximize" {
		return optimize.Maximize
	}
	return optimize.Minimize
}

func combineObjectiveAttrName(e ast.Expr) (string, bool) {
	if a, ok := e.(*ast.AttrExpr); ok {
		return a.Name, true
	}
	return "", false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	}
	return 0, false
}

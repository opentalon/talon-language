package executor

import (
	"context"
	"fmt"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/optimize"
	"github.com/opentalon/tln-language/internal/planner"
)

// execOptimizeILP runs the exact branch-and-bound solver for single-objective
// linear subset selection. Multi-objective combine routes to optimize_ga
// (the validator rejects multi-objective + solver=linear up front).
func (e *Executor) execOptimizeILP(_ context.Context, gc *planner.GoComputation, vars map[string]any) (any, error) {
	rows, _ := vars[gc.Input].([][]any)
	if len(rows) == 0 {
		return emptyILPResult(gc.Function), nil
	}

	objClauses, ok := gc.Params["objectives"].([]ast.OptimizeClause)
	if !ok || len(objClauses) != 1 {
		return nil, fmt.Errorf("optimize_ilp: requires exactly one objective")
	}
	constraints, _ := gc.Params["constraints"].([]ast.ConstraintClause)
	selectSize, _ := intParam(gc.Params, "select_size")
	attrIdx, _ := gc.Params["attr_indices"].(map[string]int)

	// Per-row attr value columns (parallel to rows).
	values := map[string][]float64{}
	for name, idx := range attrIdx {
		col := make([]float64, len(rows))
		for r, row := range rows {
			if idx < len(row) {
				col[r], _ = toFloat(row[idx])
			}
		}
		values[name] = col
	}

	// Resolve the objective into a per-row coefficient vector. Bare attr is
	// equivalent to total(attr) since each row contributes once if selected.
	objCoef, objName, dir, err := linearObjectiveCoefs(objClauses[0], values, len(rows))
	if err != nil {
		return nil, err
	}

	// Resolve constraints similarly.
	linCons := make([]optimize.LinearConstraint, 0, len(constraints))
	for _, c := range constraints {
		coef, _, err := linearAggregateCoefs(c.Left, values, len(rows))
		if err != nil {
			return nil, fmt.Errorf("optimize_ilp: constraint: %w", err)
		}
		lit, ok := c.Right.(*ast.LiteralExpr)
		if !ok {
			return nil, fmt.Errorf("optimize_ilp: constraint RHS must be numeric literal")
		}
		rhs, ok := lit.Value.(float64)
		if !ok {
			return nil, fmt.Errorf("optimize_ilp: constraint RHS must be numeric")
		}
		linCons = append(linCons, optimize.LinearConstraint{
			Coef: coef,
			Op:   c.Op,
			Rhs:  rhs,
		})
	}

	if selectSize > len(rows) {
		selectSize = len(rows)
	}

	res := optimize.ILP(optimize.ILPProblem{
		ObjectiveCoef:      objCoef,
		ObjectiveDirection: dir,
		Constraints:        linCons,
		K:                  selectSize,
	})

	selectedIDs := make([]int, 0, len(res.Selected))
	for _, idx := range res.Selected {
		if id, ok := toInt(rows[idx][0]); ok {
			selectedIDs = append(selectedIDs, id)
		}
	}

	return map[string]any{
		"function":       gc.Function,
		"feasible":       res.Feasible,
		"selected":       selectedIDs,
		"objective":      res.Objective,
		"objective_name": objName,
		"nodes_explored": res.NodesExplored,
	}, nil
}

func linearObjectiveCoefs(
	oc ast.OptimizeClause,
	values map[string][]float64,
	n int,
) ([]float64, string, optimize.Direction, error) {
	dir := optimize.Minimize
	if oc.Direction == "maximize" {
		dir = optimize.Maximize
	}
	coef, name, err := linearAggregateCoefs(oc.Attr, values, n)
	return coef, name, dir, err
}

// linearAggregateCoefs turns a `total(attr "x")` / `count(records)` /
// bare `attr "x"` expression into the per-row coefficient vector that ILP
// expects. avg() is rejected because it's nonlinear in the subset size
// (would require special handling and the user should use solver:default).
func linearAggregateCoefs(
	e ast.Expr,
	values map[string][]float64,
	n int,
) ([]float64, string, error) {
	switch x := e.(type) {
	case *ast.AttrExpr:
		if col, ok := values[x.Name]; ok {
			return col, x.Name, nil
		}
		return zeroVec(n), x.Name, nil
	case *ast.AggregateExpr:
		switch x.Fn {
		case "total":
			attr, ok := x.Arg.(*ast.AttrExpr)
			if !ok {
				return nil, "", fmt.Errorf("total() arg must be attr")
			}
			if col, ok := values[attr.Name]; ok {
				return col, x.Fn + "(" + attr.Name + ")", nil
			}
			return zeroVec(n), x.Fn + "(" + attr.Name + ")", nil
		case "count":
			// count(records) → every selected row contributes 1.
			if x.Arg == nil {
				return onesVec(n), "count(records)", nil
			}
			// count(attr "x") → contribute 1 if attr value != 0 (presence proxy).
			attr, ok := x.Arg.(*ast.AttrExpr)
			if !ok {
				return nil, "", fmt.Errorf("count() arg must be attr or records")
			}
			col := values[attr.Name]
			vec := make([]float64, n)
			for i := 0; i < n && i < len(col); i++ {
				if col[i] != 0 {
					vec[i] = 1
				}
			}
			return vec, x.Fn + "(" + attr.Name + ")", nil
		case "avg":
			return nil, "", fmt.Errorf("avg() is nonlinear in subset size — drop solver linear to use GA")
		}
	}
	return nil, "", fmt.Errorf("unsupported expression %T", e)
}

func zeroVec(n int) []float64 { return make([]float64, n) }

func onesVec(n int) []float64 {
	v := make([]float64, n)
	for i := range v {
		v[i] = 1
	}
	return v
}

func emptyILPResult(fn string) map[string]any {
	return map[string]any{
		"function":  fn,
		"feasible":  false,
		"selected":  []int{},
		"objective": 0.0,
	}
}

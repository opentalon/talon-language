package executor

import (
	"context"
	"fmt"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/optimize"
	"github.com/opentalon/tln-language/internal/planner"
)

// execOptimizeGA runs the v2 subset-selection genetic algorithm. The candidate
// rows from the upstream Datalevin query are wrapped in a SubsetProblem whose
// objective and constraint closures read attr values via the column index map
// the planner threaded into Params. The output mirrors execOptimizePareto's
// shape, with `subsets` carrying one entry per non-dominated subset and
// `flagged` listing the union of entity IDs across the frontier.
//
// Each frontier subset's `selected` field holds the entity IDs in that subset
// so downstream Decisions can render "subset {a, b, c} chosen over {a, d, e}".
func (e *Executor) execOptimizeGA(_ context.Context, gc *planner.GoComputation, vars map[string]any) (any, error) {
	rows, _ := vars[gc.Input].([][]any)

	objClauses, ok := gc.Params["objectives"].([]ast.OptimizeClause)
	if !ok {
		return nil, fmt.Errorf("optimize_ga: missing objectives param")
	}
	constraints, _ := gc.Params["constraints"].([]ast.ConstraintClause)
	selectSize, _ := intParam(gc.Params, "select_size")
	if selectSize <= 0 {
		return nil, fmt.Errorf("optimize_ga: select_size must be > 0, got %d", selectSize)
	}
	seed, _ := gc.Params["seed"].(int64)
	attrIdx, _ := gc.Params["attr_indices"].(map[string]int)

	if len(rows) == 0 {
		return emptyGAResult(gc.Function, objClauses), nil
	}

	// Materialize each row's attr values into a flat slice keyed by attr name
	// so the GA closures don't repeatedly walk the row.
	values := make(map[string][]float64, len(attrIdx))
	for name, idx := range attrIdx {
		col := make([]float64, len(rows))
		for r, row := range rows {
			if idx < 0 || idx >= len(row) {
				col[r] = 0
				continue
			}
			v, _ := toFloat(row[idx])
			col[r] = v
		}
		values[name] = col
	}

	objs, objFns, err := buildObjectiveFns(objClauses, values, len(rows))
	if err != nil {
		return nil, err
	}
	consFns, err := buildConstraintFns(constraints, values, len(rows))
	if err != nil {
		return nil, err
	}

	if selectSize > len(rows) {
		selectSize = len(rows)
	}

	prob := optimize.NewSubsetProblem(len(rows), selectSize, objFns, consFns)
	res, stats, err := optimize.GA(prob, objs, optimize.GAConfig{Seed: seed})
	if err != nil {
		return nil, fmt.Errorf("optimize_ga: %w", err)
	}

	subsets := subsetSolutionsToMaps(res.Frontier, rows, objs)
	flagged := unionSelectedIDs(res.Frontier, rows)

	return map[string]any{
		"function":    gc.Function,
		"subsets":     subsets,
		"flagged_ids": flagged,
		"objectives":  objectivesToMaps(objs),
		"select_size": selectSize,
		"generations": len(stats),
	}, nil
}

// buildObjectiveFns turns each OptimizeClause into a closure over the
// candidate population's pre-extracted attr values. Aggregate functions
// (total/avg/count) operate on the subset's mask; bare attr references in
// v2 mode are also lifted to total() because v2 always evaluates aggregates.
func buildObjectiveFns(
	clauses []ast.OptimizeClause,
	values map[string][]float64,
	n int,
) ([]optimize.Objective, []func([]bool) float64, error) {
	objs := make([]optimize.Objective, 0, len(clauses))
	fns := make([]func([]bool) float64, 0, len(clauses))
	for _, oc := range clauses {
		name, fn, err := aggregateClosure(oc.Attr, values, n)
		if err != nil {
			return nil, nil, fmt.Errorf("objective: %w", err)
		}
		objs = append(objs, optimize.Objective{Name: name, Dir: directionFor(oc.Direction)})
		fns = append(fns, fn)
	}
	return objs, fns, nil
}

// buildConstraintFns turns each ConstraintClause into a violation-magnitude
// closure: 0 if satisfied, > 0 = (LHS - RHS) magnitude on the wrong side.
// Only literal numeric RHS is supported (validator enforces this).
func buildConstraintFns(
	clauses []ast.ConstraintClause,
	values map[string][]float64,
	n int,
) ([]func([]bool) float64, error) {
	fns := make([]func([]bool) float64, 0, len(clauses))
	for _, c := range clauses {
		_, lhs, err := aggregateClosure(c.Left, values, n)
		if err != nil {
			return nil, fmt.Errorf("constraint LHS: %w", err)
		}
		lit, ok := c.Right.(*ast.LiteralExpr)
		if !ok {
			return nil, fmt.Errorf("constraint RHS must be a numeric literal")
		}
		rhs, ok := lit.Value.(float64)
		if !ok {
			return nil, fmt.Errorf("constraint RHS must be numeric")
		}
		op := c.Op
		fns = append(fns, func(mask []bool) float64 {
			got := lhs(mask)
			return violation(op, got, rhs)
		})
	}
	return fns, nil
}

// aggregateClosure returns (name, closure) for an objective/constraint
// expression. The name is a short string suitable for Objective.Name.
//
//   - AttrExpr "x"          → name "x",     closure = total of x over subset
//   - AggregateExpr total/avg/count over AttrExpr "x" → closure with that aggregator
//   - AggregateExpr count(records)                    → closure returning |subset|
func aggregateClosure(
	e ast.Expr,
	values map[string][]float64,
	n int,
) (string, func([]bool) float64, error) {
	switch x := e.(type) {
	case *ast.AttrExpr:
		col, ok := values[x.Name]
		if !ok {
			return x.Name, zeroFn, nil
		}
		return x.Name, sumFn(col), nil
	case *ast.AggregateExpr:
		if x.Arg == nil && x.Fn == "count" {
			return "count(records)", func(mask []bool) float64 {
				c := 0.0
				for _, v := range mask {
					if v {
						c++
					}
				}
				return c
			}, nil
		}
		attr, ok := x.Arg.(*ast.AttrExpr)
		if !ok {
			return "", nil, fmt.Errorf("aggregate arg must be attr \"name\"")
		}
		col, ok := values[attr.Name]
		if !ok {
			return x.Fn + "(" + attr.Name + ")", zeroFn, nil
		}
		name := x.Fn + "(" + attr.Name + ")"
		switch x.Fn {
		case "total":
			return name, sumFn(col), nil
		case "avg":
			return name, avgFn(col), nil
		case "count":
			// count(attr "x") = count of rows where attr is present and selected
			return name, func(mask []bool) float64 {
				c := 0.0
				for i, v := range mask {
					if v && col[i] != 0 {
						c++
					}
				}
				return c
			}, nil
		default:
			return name, nil, fmt.Errorf("unknown aggregate %q", x.Fn)
		}
	}
	return "", nil, fmt.Errorf("unsupported objective/constraint expression %T", e)
}

func sumFn(col []float64) func([]bool) float64 {
	return func(mask []bool) float64 {
		sum := 0.0
		for i, v := range mask {
			if v {
				sum += col[i]
			}
		}
		return sum
	}
}

func avgFn(col []float64) func([]bool) float64 {
	return func(mask []bool) float64 {
		sum, c := 0.0, 0
		for i, v := range mask {
			if v {
				sum += col[i]
				c++
			}
		}
		if c == 0 {
			return 0
		}
		return sum / float64(c)
	}
}

func zeroFn(_ []bool) float64 { return 0 }

// violation returns 0 if the comparison holds, otherwise the magnitude of
// how much it failed by. Standard for penalty/repair-style constraint handling.
func violation(op string, got, want float64) float64 {
	switch op {
	case "<=":
		if got <= want {
			return 0
		}
		return got - want
	case "<":
		if got < want {
			return 0
		}
		return got - want
	case ">=":
		if got >= want {
			return 0
		}
		return want - got
	case ">":
		if got > want {
			return 0
		}
		return want - got
	case "==":
		if got == want {
			return 0
		}
		return abs(got - want)
	case "!=":
		if got != want {
			return 0
		}
		return 1
	}
	return 0
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func subsetSolutionsToMaps(sols []optimize.Solution, rows [][]any, objs []optimize.Objective) []map[string]any {
	out := make([]map[string]any, 0, len(sols))
	for _, s := range sols {
		mask, _ := s.Row.([]bool)
		selected := selectedEntityIDs(mask, rows)
		values := map[string]any{}
		for i, obj := range objs {
			values[obj.Name] = s.Values[i]
		}
		out = append(out, map[string]any{
			"rank":      s.Rank,
			"crowding":  s.CrowdingDist,
			"dominated": s.DominatedCount,
			"selected":  selected,
			"values":    values,
		})
	}
	return out
}

func unionSelectedIDs(sols []optimize.Solution, rows [][]any) []int {
	seen := map[int]bool{}
	var out []int
	for _, s := range sols {
		mask, _ := s.Row.([]bool)
		for _, id := range selectedEntityIDs(mask, rows) {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

func selectedEntityIDs(mask []bool, rows [][]any) []int {
	out := make([]int, 0)
	for i, v := range mask {
		if !v || i >= len(rows) {
			continue
		}
		if len(rows[i]) == 0 {
			continue
		}
		if id, ok := toInt(rows[i][0]); ok {
			out = append(out, id)
		}
	}
	return out
}

func emptyGAResult(fn string, objClauses []ast.OptimizeClause) map[string]any {
	objs := make([]optimize.Objective, len(objClauses))
	for i, oc := range objClauses {
		name, _ := combineObjectiveAttrName(oc.Attr)
		objs[i] = optimize.Objective{Name: name, Dir: directionFor(oc.Direction)}
	}
	return map[string]any{
		"function":    fn,
		"subsets":     []map[string]any{},
		"flagged_ids": []int{},
		"objectives":  objectivesToMaps(objs),
	}
}

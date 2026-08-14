package testrunner

import (
	"fmt"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/explain"
	"github.com/opentalon/tln-language/internal/optimize"
	"github.com/opentalon/tln-language/internal/planner"
)

// ─── ACO ──────────────────────────────────────────────────────────────────────

type acoNarrowing struct {
	tour    []int
	length  float64
	flagged []int
	posByID map[int]int // entity_id → position in tour (0 = first stop)
	xName   string
	yName   string
	result  map[string]any
}

// narrowByACO computes the tour over the in-memory entity set using the
// two coordinate attrs the planner passed in. Returns the entity IDs in
// tour order as the new flagged set.
func narrowByACO(gc *planner.GoComputation, flagged []int, entities map[int]*entity) acoNarrowing {
	xName, _ := gc.Params["x_attr"].(string)
	yName, _ := gc.Params["y_attr"].(string)
	seed, _ := gc.Params["seed"].(int64)
	if len(flagged) == 0 || xName == "" || yName == "" {
		return acoNarrowing{result: stubACOResult(gc)}
	}

	xs := make([]float64, len(flagged))
	ys := make([]float64, len(flagged))
	for i, id := range flagged {
		ent := entities[id]
		if ent == nil {
			continue
		}
		if v, ok := numericValue(ent.fields[":attr/"+xName]); ok {
			xs[i] = v
		}
		if v, ok := numericValue(ent.fields[":attr/"+yName]); ok {
			ys[i] = v
		}
	}

	dist := optimize.EuclideanDistanceMatrix(xs, ys)
	res := optimize.ACO(dist, optimize.ACOConfig{Seed: seed})

	tourIDs := make([]int, len(res.Tour))
	posByID := map[int]int{}
	for pos, idx := range res.Tour {
		tourIDs[pos] = flagged[idx]
		posByID[flagged[idx]] = pos
	}

	return acoNarrowing{
		tour:    tourIDs,
		length:  res.Length,
		flagged: tourIDs,
		posByID: posByID,
		xName:   xName,
		yName:   yName,
		result: map[string]any{
			"function":   planner.FuncOptimizeACO,
			"tour":       tourIDs,
			"length":     res.Length,
			"iterations": len(res.History),
		},
	}
}

func acoEvidence(entityID int, n acoNarrowing) []explain.Fact {
	pos, ok := n.posByID[entityID]
	if !ok {
		return nil
	}
	return []explain.Fact{
		{Attribute: "stop_number", Value: pos + 1},
		{Attribute: "tour_length", Value: n.length},
		{Attribute: "total_stops", Value: len(n.tour)},
	}
}

func acoWhy(entityID int, n acoNarrowing) []string {
	pos, ok := n.posByID[entityID]
	if !ok {
		return nil
	}
	return []string{
		fmt.Sprintf("stop %d of %d on the shortest tour (length %.2f via (%s, %s))",
			pos+1, len(n.tour), n.length, n.xName, n.yName),
	}
}

func stubACOResult(gc *planner.GoComputation) map[string]any {
	return map[string]any{
		"function": gc.Function,
		"status":   "stub",
	}
}

// ─── ILP ──────────────────────────────────────────────────────────────────────

type ilpNarrowing struct {
	flagged   []int
	feasible  bool
	objective float64
	objName   string
	result    map[string]any
}

// narrowByILP solves the exact 0/1 subset problem against the in-memory
// entity set and returns the optimal selection as the flagged set.
func narrowByILP(gc *planner.GoComputation, flagged []int, entities map[int]*entity) ilpNarrowing {
	if len(flagged) == 0 {
		return ilpNarrowing{result: stubILPResult(gc)}
	}
	objClauses, _ := gc.Params["objectives"].([]ast.OptimizeClause)
	if len(objClauses) != 1 {
		return ilpNarrowing{result: stubILPResult(gc)}
	}
	constraints, _ := gc.Params["constraints"].([]ast.ConstraintClause)
	selectSize, _ := intParamGA(gc.Params, "select_size")

	// Build per-row attr value vectors from the in-memory store.
	attrNames := collectAttrNames(objClauses, constraints)
	values := map[string][]float64{}
	for _, name := range attrNames {
		col := make([]float64, len(flagged))
		for i, id := range flagged {
			ent := entities[id]
			if ent == nil {
				continue
			}
			if v, ok := numericValue(ent.fields[":attr/"+name]); ok {
				col[i] = v
			}
		}
		values[name] = col
	}

	objCoef, objName, dir, err := linearObjectiveCoefsTR(objClauses[0], values, len(flagged))
	if err != nil {
		return ilpNarrowing{result: stubILPResult(gc)}
	}

	linCons := make([]optimize.LinearConstraint, 0, len(constraints))
	for _, c := range constraints {
		coef, _, err := linearAggregateCoefsTR(c.Left, values, len(flagged))
		if err != nil {
			return ilpNarrowing{result: stubILPResult(gc)}
		}
		lit, ok := c.Right.(*ast.LiteralExpr)
		if !ok {
			return ilpNarrowing{result: stubILPResult(gc)}
		}
		rhs, ok := lit.Value.(float64)
		if !ok {
			return ilpNarrowing{result: stubILPResult(gc)}
		}
		linCons = append(linCons, optimize.LinearConstraint{Coef: coef, Op: c.Op, Rhs: rhs})
	}

	if selectSize > len(flagged) {
		selectSize = len(flagged)
	}

	res := optimize.ILP(optimize.ILPProblem{
		ObjectiveCoef:      objCoef,
		ObjectiveDirection: dir,
		Constraints:        linCons,
		K:                  selectSize,
	})

	selected := make([]int, 0, len(res.Selected))
	for _, idx := range res.Selected {
		selected = append(selected, flagged[idx])
	}

	return ilpNarrowing{
		flagged:   selected,
		feasible:  res.Feasible,
		objective: res.Objective,
		objName:   objName,
		result: map[string]any{
			"function":       planner.FuncOptimizeILP,
			"feasible":       res.Feasible,
			"selected":       selected,
			"objective":      res.Objective,
			"objective_name": objName,
		},
	}
}

func ilpEvidence(entityID int, n ilpNarrowing) []explain.Fact {
	return []explain.Fact{
		{Attribute: "exact_optimum", Value: true},
		{Attribute: n.objName, Value: n.objective},
		{Attribute: "subset_size", Value: len(n.flagged)},
	}
}

func ilpWhy(entityID int, n ilpNarrowing) []string {
	return []string{
		fmt.Sprintf("part of the provably optimal subset (%s = %.4g)", n.objName, n.objective),
	}
}

func stubILPResult(gc *planner.GoComputation) map[string]any {
	return map[string]any{
		"function": gc.Function,
		"status":   "stub",
	}
}

// ─── shared coefficient builders for ILP ──────────────────────────────────────

func linearObjectiveCoefsTR(
	oc ast.OptimizeClause,
	values map[string][]float64,
	n int,
) ([]float64, string, optimize.Direction, error) {
	dir := optimize.Minimize
	if oc.Direction == "maximize" {
		dir = optimize.Maximize
	}
	coef, name, err := linearAggregateCoefsTR(oc.Attr, values, n)
	return coef, name, dir, err
}

func linearAggregateCoefsTR(
	e ast.Expr,
	values map[string][]float64,
	n int,
) ([]float64, string, error) {
	switch x := e.(type) {
	case *ast.AttrExpr:
		col := values[x.Name]
		if col == nil {
			col = make([]float64, n)
		}
		return col, x.Name, nil
	case *ast.AggregateExpr:
		switch x.Fn {
		case "total":
			attr, ok := x.Arg.(*ast.AttrExpr)
			if !ok {
				return nil, "", fmt.Errorf("total() arg must be attr")
			}
			col := values[attr.Name]
			if col == nil {
				col = make([]float64, n)
			}
			return col, x.Fn + "(" + attr.Name + ")", nil
		case "count":
			if x.Arg == nil {
				vec := make([]float64, n)
				for i := range vec {
					vec[i] = 1
				}
				return vec, "count(records)", nil
			}
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
			return nil, "", fmt.Errorf("avg() is nonlinear — drop solver linear")
		}
	}
	return nil, "", fmt.Errorf("unsupported expression %T", e)
}

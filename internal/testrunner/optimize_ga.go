package testrunner

import (
	"fmt"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/explain"
	"github.com/opentalon/talon-language/internal/optimize"
	"github.com/opentalon/talon-language/internal/planner"
)

// gaNarrowing is the testrunner-side result of executing an optimize_ga
// GoComputation against in-memory entities. `flagged` is the union of entity
// IDs across all rank-0 subsets; bySubsetID maps each frontier subset's index
// to the entity-ID list it selected so per-entity Decisions can cite "which
// subset chose me." `result` is the vars-bag payload mirroring the executor.
type gaNarrowing struct {
	flagged         []int
	subsets         []gaSubset
	objs            []optimize.Objective
	entityToSubsets map[int][]int // entity_id → indices into subsets
	violationByMask map[string]float64
	result          map[string]any
}

type gaSubset struct {
	Index    int
	Rank     int
	Crowding float64
	Selected []int
	Values   map[string]float64
}

// narrowByGA evaluates a FuncOptimizeGA GoComputation against the in-memory
// entity store. Returns the flagged set (union of frontier subsets) plus
// per-subset metadata for Decision rendering.
func narrowByGA(gc *planner.GoComputation, flagged []int, entities map[int]*entity) gaNarrowing {
	objClauses, _ := gc.Params["objectives"].([]ast.OptimizeClause)
	constraints, _ := gc.Params["constraints"].([]ast.ConstraintClause)
	selectSize, _ := intParamGA(gc.Params, "select_size")
	seed, _ := gc.Params["seed"].(int64)

	if len(flagged) == 0 || selectSize <= 0 {
		return gaNarrowing{result: stubGAResult(gc)}
	}

	// Materialize per-entity attr values. We collect the names referenced
	// by objectives + constraints (validator/planner guarantee these resolve
	// to AttrExpr arms) and read directly from the entity store.
	attrNames := collectAttrNames(objClauses, constraints)
	values := map[string][]float64{}
	for _, name := range attrNames {
		col := make([]float64, len(flagged))
		for i, id := range flagged {
			ent := entities[id]
			if ent == nil {
				continue
			}
			if raw, ok := ent.fields[":attr/"+name]; ok {
				if v, ok := numericValue(raw); ok {
					col[i] = v
				}
			}
		}
		values[name] = col
	}

	objs, objFns, err := buildObjectiveFnsTR(objClauses, values, len(flagged))
	if err != nil {
		return gaNarrowing{result: stubGAResult(gc)}
	}
	consFns, err := buildConstraintFnsTR(constraints, values, len(flagged))
	if err != nil {
		return gaNarrowing{result: stubGAResult(gc)}
	}
	if selectSize > len(flagged) {
		selectSize = len(flagged)
	}

	prob := optimize.NewSubsetProblem(len(flagged), selectSize, objFns, consFns)
	res, _, err := optimize.GA(prob, objs, optimize.GAConfig{Seed: seed})
	if err != nil {
		return gaNarrowing{result: stubGAResult(gc)}
	}

	subsets := make([]gaSubset, 0, len(res.Frontier))
	entityToSubsets := map[int][]int{}
	var newFlagged []int
	seen := map[int]bool{}
	for idx, s := range res.Frontier {
		mask, _ := s.Row.([]bool)
		selected := make([]int, 0, selectSize)
		for i, v := range mask {
			if v && i < len(flagged) {
				selected = append(selected, flagged[i])
				if !seen[flagged[i]] {
					seen[flagged[i]] = true
					newFlagged = append(newFlagged, flagged[i])
				}
				entityToSubsets[flagged[i]] = append(entityToSubsets[flagged[i]], idx)
			}
		}
		valuesMap := map[string]float64{}
		for i, obj := range objs {
			valuesMap[obj.Name] = s.Values[i]
		}
		subsets = append(subsets, gaSubset{
			Index:    idx,
			Rank:     s.Rank,
			Crowding: s.CrowdingDist,
			Selected: selected,
			Values:   valuesMap,
		})
	}

	return gaNarrowing{
		flagged:         newFlagged,
		subsets:         subsets,
		objs:            objs,
		entityToSubsets: entityToSubsets,
		result: map[string]any{
			"function":    planner.FuncOptimizeGA,
			"subsets":     subsetsForVars(subsets),
			"flagged_ids": newFlagged,
			"objectives":  objectivesForVars(objs),
			"select_size": selectSize,
		},
	}
}

// gaEvidence renders per-entity evidence facts for a selected entity:
// which subset(s) chose it, the subset's rank, and each objective's value
// in the (lowest-rank, highest-crowding) subset.
func gaEvidence(entityID int, n gaNarrowing) []explain.Fact {
	idxs := n.entityToSubsets[entityID]
	if len(idxs) == 0 {
		return nil
	}
	best := n.subsets[idxs[0]]
	for _, i := range idxs[1:] {
		s := n.subsets[i]
		if s.Rank < best.Rank || (s.Rank == best.Rank && s.Crowding > best.Crowding) {
			best = s
		}
	}
	facts := make([]explain.Fact, 0, len(best.Values)+3)
	for _, obj := range n.objs {
		facts = append(facts, explain.Fact{
			Attribute: "subset_" + obj.Name,
			Value:     best.Values[obj.Name],
		})
	}
	facts = append(facts,
		explain.Fact{Attribute: "subset_rank", Value: best.Rank},
		explain.Fact{Attribute: "subset_size", Value: len(best.Selected)},
		explain.Fact{Attribute: "subset_members", Value: intsToCSV(best.Selected)},
	)
	return facts
}

// gaWhy renders the "this entity was selected as part of subset X" line.
// Mentions how many subsets selected it (out of total frontier subsets)
// so domain experts can see whether the choice was robust.
func gaWhy(entityID int, n gaNarrowing) []string {
	idxs := n.entityToSubsets[entityID]
	if len(idxs) == 0 {
		return nil
	}
	names := ""
	for i, o := range n.objs {
		if i > 0 {
			names += ", "
		}
		names += o.Name
	}
	return []string{
		fmt.Sprintf("selected in %d of %d Pareto-optimal subsets on (%s)",
			len(idxs), len(n.subsets), names),
	}
}

func collectAttrNames(objs []ast.OptimizeClause, cons []ast.ConstraintClause) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, o := range objs {
		add(attrNameOf(o.Attr))
	}
	for _, c := range cons {
		add(attrNameOf(c.Left))
	}
	return out
}

func attrNameOf(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.AttrExpr:
		return x.Name
	case *ast.AggregateExpr:
		if a, ok := x.Arg.(*ast.AttrExpr); ok {
			return a.Name
		}
	}
	return ""
}

func buildObjectiveFnsTR(
	clauses []ast.OptimizeClause,
	values map[string][]float64,
	n int,
) ([]optimize.Objective, []func([]bool) float64, error) {
	objs := make([]optimize.Objective, 0, len(clauses))
	fns := make([]func([]bool) float64, 0, len(clauses))
	for _, oc := range clauses {
		name, fn, err := aggregateClosureTR(oc.Attr, values)
		if err != nil {
			return nil, nil, err
		}
		objs = append(objs, optimize.Objective{Name: name, Dir: testrunnerDirectionFor(oc.Direction)})
		fns = append(fns, fn)
	}
	return objs, fns, nil
}

func buildConstraintFnsTR(
	clauses []ast.ConstraintClause,
	values map[string][]float64,
	n int,
) ([]func([]bool) float64, error) {
	fns := make([]func([]bool) float64, 0, len(clauses))
	for _, c := range clauses {
		_, lhs, err := aggregateClosureTR(c.Left, values)
		if err != nil {
			return nil, err
		}
		lit, ok := c.Right.(*ast.LiteralExpr)
		if !ok {
			return nil, fmt.Errorf("constraint RHS must be a literal")
		}
		rhs, ok := lit.Value.(float64)
		if !ok {
			return nil, fmt.Errorf("constraint RHS must be numeric")
		}
		op := c.Op
		fns = append(fns, func(mask []bool) float64 {
			got := lhs(mask)
			return violationTR(op, got, rhs)
		})
	}
	return fns, nil
}

func aggregateClosureTR(
	e ast.Expr,
	values map[string][]float64,
) (string, func([]bool) float64, error) {
	switch x := e.(type) {
	case *ast.AttrExpr:
		col := values[x.Name]
		return x.Name, sumFnTR(col), nil
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
		col := values[attr.Name]
		name := x.Fn + "(" + attr.Name + ")"
		switch x.Fn {
		case "total":
			return name, sumFnTR(col), nil
		case "avg":
			return name, avgFnTR(col), nil
		case "count":
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
	return "", nil, fmt.Errorf("unsupported expression %T", e)
}

func sumFnTR(col []float64) func([]bool) float64 {
	return func(mask []bool) float64 {
		s := 0.0
		for i, v := range mask {
			if v {
				s += col[i]
			}
		}
		return s
	}
}

func avgFnTR(col []float64) func([]bool) float64 {
	return func(mask []bool) float64 {
		s, c := 0.0, 0
		for i, v := range mask {
			if v {
				s += col[i]
				c++
			}
		}
		if c == 0 {
			return 0
		}
		return s / float64(c)
	}
}

func violationTR(op string, got, want float64) float64 {
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
		return absTR(got - want)
	case "!=":
		if got != want {
			return 0
		}
		return 1
	}
	return 0
}

func absTR(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func subsetsForVars(subs []gaSubset) []map[string]any {
	out := make([]map[string]any, 0, len(subs))
	for _, s := range subs {
		values := map[string]any{}
		for k, v := range s.Values {
			values[k] = v
		}
		out = append(out, map[string]any{
			"index":    s.Index,
			"rank":     s.Rank,
			"crowding": s.Crowding,
			"selected": s.Selected,
			"values":   values,
		})
	}
	return out
}

func intParamGA(params map[string]any, key string) (int, bool) {
	switch v := params[key].(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	}
	return 0, false
}

func intsToCSV(ints []int) string {
	out := ""
	for i, n := range ints {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprintf("%d", n)
	}
	return out
}

func stubGAResult(gc *planner.GoComputation) map[string]any {
	return map[string]any{
		"function": gc.Function,
		"params":   gc.Params,
		"status":   "stub",
	}
}

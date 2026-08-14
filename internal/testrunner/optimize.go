package testrunner

import (
	"fmt"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/explain"
	"github.com/opentalon/tln-language/internal/optimize"
	"github.com/opentalon/tln-language/internal/planner"
)

// paretoNarrowing holds the testrunner-side result of executing an
// optimize_pareto GoComputation against the in-memory entity store.
// `frontier` is the rank-0 entity IDs (becomes the new flagged set);
// `byEntity` is the per-entity ranked solution for evidence rendering;
// `result` is the vars-bag payload mirroring the executor's output shape.
type paretoNarrowing struct {
	frontier []int
	byEntity map[int]optimize.Solution
	objs     []optimize.Objective
	result   map[string]any
}

// narrowByPareto evaluates a FuncOptimizePareto GoComputation against the
// in-memory entity set. Returns rank-0 entity IDs as the new flagged set;
// rows missing or with non-numeric values in any objective slot are skipped.
func narrowByPareto(gc *planner.GoComputation, flagged []int, entities map[int]*entity) paretoNarrowing {
	objClauses, _ := gc.Params["objectives"].([]ast.OptimizeClause)
	if len(objClauses) == 0 {
		return paretoNarrowing{result: stubResult(gc)}
	}

	objs := make([]optimize.Objective, 0, len(objClauses))
	attrPaths := make([]string, 0, len(objClauses))
	for _, oc := range objClauses {
		attr, ok := oc.Attr.(*ast.AttrExpr)
		if !ok {
			return paretoNarrowing{result: stubResult(gc)}
		}
		objs = append(objs, optimize.Objective{
			Name: attr.Name,
			Dir:  testrunnerDirectionFor(oc.Direction),
		})
		attrPaths = append(attrPaths, ":attr/"+attr.Name)
	}

	individuals := make([]optimize.Individual, 0, len(flagged))
	for _, id := range flagged {
		ent := entities[id]
		if ent == nil {
			continue
		}
		values := make([]float64, 0, len(attrPaths))
		skip := false
		for _, path := range attrPaths {
			raw, ok := ent.fields[path]
			if !ok {
				skip = true
				break
			}
			v, ok := numericValue(raw)
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
			EntityID: id,
			Values:   values,
		})
	}

	res, err := optimize.Pareto(individuals, objs)
	if err != nil {
		return paretoNarrowing{result: stubResult(gc)}
	}

	frontier := make([]int, 0, len(res.Frontier))
	for _, s := range res.Frontier {
		frontier = append(frontier, s.EntityID)
	}

	byEntity := map[int]optimize.Solution{}
	for _, s := range res.All {
		byEntity[s.EntityID] = s
	}

	return paretoNarrowing{
		frontier: frontier,
		byEntity: byEntity,
		objs:     objs,
		result: map[string]any{
			"function":   planner.FuncOptimizePareto,
			"frontier":   solutionsForVars(res.Frontier, objs),
			"all":        solutionsForVars(res.All, objs),
			"objectives": objectivesForVars(objs),
		},
	}
}

// paretoEvidence formats per-entity evidence facts for a rank-0 solution:
// one Fact per objective (showing the entity's value on that axis), plus
// the rank, dominated count, and crowding distance.
func paretoEvidence(sol optimize.Solution, objs []optimize.Objective) []explain.Fact {
	facts := make([]explain.Fact, 0, len(objs)+3)
	for i, obj := range objs {
		facts = append(facts, explain.Fact{
			Attribute: obj.Name,
			Value:     sol.Values[i],
		})
	}
	facts = append(facts,
		explain.Fact{Attribute: "pareto_rank", Value: sol.Rank},
		explain.Fact{Attribute: "dominated_by", Value: sol.DominatedCount},
		explain.Fact{Attribute: "crowding_distance", Value: sol.CrowdingDist},
	)
	return facts
}

// paretoWhy renders the population-relative "why this entity is on the
// frontier" line that tln explain shows. Includes the objective names
// and how many other candidates this one dominates / is dominated by.
func paretoWhy(sol optimize.Solution, objs []optimize.Objective, populationSize int) []string {
	names := ""
	for i, o := range objs {
		if i > 0 {
			names += ", "
		}
		names += o.Name
	}
	return []string{
		fmt.Sprintf("non-dominated on (%s); rank %d; dominated %d of %d candidates",
			names, sol.Rank, sol.DominatedCount, populationSize-1),
	}
}

func testrunnerDirectionFor(s string) optimize.Direction {
	if s == "maximize" {
		return optimize.Maximize
	}
	return optimize.Minimize
}

func solutionsForVars(sols []optimize.Solution, objs []optimize.Objective) []map[string]any {
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
		})
	}
	return out
}

func objectivesForVars(objs []optimize.Objective) []map[string]any {
	out := make([]map[string]any, 0, len(objs))
	for _, o := range objs {
		out = append(out, map[string]any{"name": o.Name, "direction": o.Dir.String()})
	}
	return out
}

func stubResult(gc *planner.GoComputation) map[string]any {
	return map[string]any{
		"function": gc.Function,
		"params":   gc.Params,
		"status":   "stub",
	}
}

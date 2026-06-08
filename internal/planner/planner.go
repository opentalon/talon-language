package planner

import (
	"fmt"
	"strings"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/diagnostic"
	"github.com/opentalon/talon-language/internal/factstore"
)

// GoComputation function names.
const (
	FuncAnomalyZscore        = "anomaly_zscore"
	FuncAnomalyGrubbs        = "anomaly_grubbs"
	FuncLearnedThreshold     = "learned_threshold"
	FuncPredictDecisionTree  = "predict_decision_tree"
	FuncForecastExpSmoothing = "forecast_exponential_smoothing"
	FuncClusterDBSCAN        = "cluster_dbscan"
	FuncSimilarityCosine     = "similarity_cosine"
	FuncClassifyKNN          = "classify_knn"
	FuncPPRTopK              = "ppr_topk"
	FuncRenderTemplate       = "render_template"
	FuncOptimizePareto       = "optimize_pareto"
	FuncOptimizeGA           = "optimize_ga"
	FuncOptimizeACO          = "optimize_aco"
	FuncOptimizeILP          = "optimize_ilp"
)

// ─── Plan step types ──────────────────────────────────────────────────────────

// QueryPlan is the execution plan for one Talon block.
type QueryPlan struct {
	BlockName string
	Steps     []PlanStep
}

// PlanStep is implemented by FactQuery, GoComputation, MLComputation, and Filter.
type PlanStep interface {
	stepType() string
}

// FactQuery is a structured read step against any FactStore implementation.
// The planner emits these; the executor passes them to the backend. The
// Datalevin client translates the structured form to its native Datalog at
// the call boundary; MemoryStore interprets the form directly.
type FactQuery struct {
	Query    factstore.Query // structured query over patterns/predicates
	BindVars map[string]any  // parameters bound at query time (reserved)
	Into     string          // result variable name
}

// GoComputation runs a non-ML Go function over a result set
// (template rendering, block-match resolution, MCP calls, combinatorial optimize).
type GoComputation struct {
	Function string         // function name constant (FuncXxx)
	Input    string         // result variable from a previous step
	Params   map[string]any // function parameters
	Into     string         // output variable name
}

// MLComputation runs an ML primitive (one of the 7 keywords).
// The result row carries an explanation alongside its value so downstream steps
// and `talon trace` can surface the decision path. See ADR-0001.
type MLComputation struct {
	Function string         // function name constant (FuncXxx, ML subset)
	Input    string         // result variable from a previous step
	Params   map[string]any // primitive parameters (features, window, series…)
	Into     string         // output variable name
}

// Filter applies a Go-side predicate to a result set.
type Filter struct {
	Input     string
	Condition string // Go predicate expression
	Into      string
}

// GraphSnapshot is a plan step that asks the executor to build (or load
// from cache) a *factstore.GraphSnapshot and bind it to a variable for
// downstream PPR computation. The Options field carries the same fields
// as factstore.SnapshotOptions, but is left as map[string]any to keep
// the planner free of factstore imports — the executor unpacks it.
type GraphSnapshot struct {
	CacheKey string
	Options  map[string]any
	Into     string
}

func (*FactQuery) stepType() string { return "FactQuery" }
func (*GoComputation) stepType() string  { return "GoComputation" }
func (*MLComputation) stepType() string  { return "MLComputation" }
func (*Filter) stepType() string         { return "Filter" }
func (*GraphSnapshot) stepType() string  { return "GraphSnapshot" }

// anomalyFunctionFor maps an `is anomaly using <METHOD>` method string to
// the planner function constant the executor dispatches on. Empty string
// (no `using` clause given) falls back to z-score for back-compat with
// the original `is anomaly compared_to ...` syntax.
func anomalyFunctionFor(method string) string {
	switch method {
	case "grubbs":
		return FuncAnomalyGrubbs
	case "", "zscore":
		return FuncAnomalyZscore
	}
	// Unknown method — validator already reported the error. Falling back
	// to z-score keeps the plan well-formed so downstream steps can run.
	return FuncAnomalyZscore
}

// IsMLFunction reports whether fn names one of the 7 ML primitives.
func IsMLFunction(fn string) bool {
	switch fn {
	case FuncAnomalyZscore,
		FuncAnomalyGrubbs,
		FuncLearnedThreshold,
		FuncPredictDecisionTree,
		FuncForecastExpSmoothing,
		FuncClusterDBSCAN,
		FuncSimilarityCosine,
		FuncClassifyKNN,
		FuncPPRTopK:
		return true
	}
	return false
}

// QueryOf returns the structured query for a FactQuery step. Kept as a
// helper so external callers don't dereference the field directly (room
// to swap representations later).
func QueryOf(q *FactQuery) factstore.Query {
	return q.Query
}

// ─── Entry point ──────────────────────────────────────────────────────────────

// Plan compiles a validated AST into per-block QueryPlans.
// define blocks are inlined at compile time and produce no plan of their own.
func Plan(prog *ast.Program) (map[string]*QueryPlan, diagnostic.List) {
	p := &planner{
		prog:    prog,
		defines: collectDefines(prog),
	}
	return p.planAll()
}

type planner struct {
	prog    *ast.Program
	defines map[string]*ast.DefineBlock
	diags   diagnostic.List
}

func collectDefines(prog *ast.Program) map[string]*ast.DefineBlock {
	m := map[string]*ast.DefineBlock{}
	for _, b := range prog.Blocks {
		if def, ok := b.(*ast.DefineBlock); ok {
			m[def.Name] = def
		}
	}
	return m
}

func (p *planner) planAll() (map[string]*QueryPlan, diagnostic.List) {
	plans := map[string]*QueryPlan{}
	for _, b := range p.prog.Blocks {
		switch bb := b.(type) {
		case *ast.DefineBlock:
			// defines are inlined; no standalone plan
		case *ast.DetectBlock:
			plans[bb.Name] = p.planDetect(bb)
		case *ast.RuleBlock:
			plans[bb.Name] = p.planRule(bb)
		case *ast.RecommendBlock:
			plans[bb.Name] = p.planRecommend(bb)
		case *ast.PredictBlock:
			plans[bb.Name] = p.planPredictBlock(bb)
		case *ast.ForecastBlock:
			plans[bb.Name] = p.planForecastBlock(bb)
		case *ast.ClusterBlock:
			plans[bb.Name] = p.planClusterBlock(bb)
		case *ast.ClassifyBlock:
			plans[bb.Name] = p.planClassifyBlock(bb)
		case *ast.SimilarBlock:
			plans[bb.Name] = p.planSimilarBlock(bb)
		case *ast.RelatedBlock:
			plans[bb.Name] = p.planRelatedBlock(bb)
		case *ast.CombineBlock:
			plans[bb.Name] = p.planCombine(bb)
		case *ast.WorkflowBlock:
			plans[bb.Name] = p.planWorkflow(bb)
		}
	}
	return plans, p.diags
}

// ─── Block planners ───────────────────────────────────────────────────────────

func (p *planner) planDetect(b *ast.DetectBlock) *QueryPlan {
	plan := &QueryPlan{BlockName: b.Name}

	qb := p.newQueryBuilder()
	qb.addSelector(b.Selector)

	plan.Steps = append(plan.Steps, &FactQuery{
		Query:    qb.build(),
		BindVars: qb.bindVars(),
		Into:     "candidates",
	})
	last := "candidates"

	if len(qb.goConditions) > 0 {
		plan.Steps = append(plan.Steps, &Filter{
			Input:     last,
			Condition: renderGoConditions(qb.goConditions),
			Into:      "filtered",
		})
		last = "filtered"
	}

	for i, ab := range qb.anomalyConds {
		into := fmt.Sprintf("anomaly_%d", i)
		plan.Steps = append(plan.Steps, &MLComputation{
			Function: anomalyFunctionFor(ab.Method),
			Input:    last,
			Params: map[string]any{
				"attr":        ab.AttrName,
				"window":      ab.Window,
				"value_index": indexOf(qb.findVars, ab.ValueVar),
			},
			Into: into,
		})
		last = into
	}

	for i, tb := range qb.thresholdConds {
		into := fmt.Sprintf("threshold_%d", i)
		plan.Steps = append(plan.Steps, &MLComputation{
			Function: FuncLearnedThreshold,
			Input:    last,
			Params: map[string]any{
				"attr":        tb.AttrName,
				"method":      tb.Method,
				"op":          tb.Op,
				"window":      tb.Window,
				"value_index": indexOf(qb.findVars, tb.ValueVar),
			},
			Into: into,
		})
		last = into
	}

	if b.Anomaly != nil {
		plan.Steps = append(plan.Steps, &MLComputation{
			Function: anomalyFunctionFor(b.Anomaly.Method),
			Input:    last,
			Params:   map[string]any{"window": b.Anomaly.Window},
			Into:     "anomaly_results",
		})
		last = "anomaly_results"
	}

	if b.Predict != nil {
		plan.Steps = append(plan.Steps, &MLComputation{
			Function: FuncPredictDecisionTree,
			Input:    last,
			Params:   map[string]any{"features": b.Predict.Features},
			Into:     "predictions",
		})
		last = "predictions"
	}

	if b.Forecast != nil {
		plan.Steps = append(plan.Steps, &MLComputation{
			Function: FuncForecastExpSmoothing,
			Input:    last,
			Params:   map[string]any{"series": b.Forecast.Series},
			Into:     "forecast_results",
		})
		last = "forecast_results"
	}

	if b.Related != nil {
		plan.Steps = append(plan.Steps, &GraphSnapshot{
			CacheKey: b.Name,
			Options:  map[string]any{},
			Into:     "graph",
		})
		plan.Steps = append(plan.Steps, &MLComputation{
			Function: FuncPPRTopK,
			Input:    last,
			Params: map[string]any{
				"graph_var":      "graph",
				"seed_expr":      b.Related.To,
				"seeds_expr":     b.Related.Seeds,
				"top_k":          ptrOrNil(b.Related.TopK),
				"damping":        ptrOrNil(b.Related.Damping),
				"flag_predicate": "topk",
			},
			Into: "related_records",
		})
		last = "related_records"
	}

	if b.Label != nil {
		plan.Steps = append(plan.Steps, &GoComputation{
			Function: FuncRenderTemplate,
			Input:    last,
			Params:   map[string]any{"template": b.Label.Raw},
			Into:     "detections",
		})
	}

	return plan
}

func (p *planner) planRule(b *ast.RuleBlock) *QueryPlan {
	plan := &QueryPlan{BlockName: b.Name}
	qb := p.newQueryBuilder()
	if b.Selector != nil {
		qb.addSelector(*b.Selector)
	} else if b.When != nil {
		qb.addCondition(b.When)
	}
	plan.Steps = append(plan.Steps, &FactQuery{
		Query:    qb.build(),
		BindVars: qb.bindVars(),
		Into:     "candidates",
	})
	last := "candidates"
	if len(qb.goConditions) > 0 {
		plan.Steps = append(plan.Steps, &Filter{
			Input:     last,
			Condition: renderGoConditions(qb.goConditions),
			Into:      "policy_candidates",
		})
	}
	return plan
}

func (p *planner) planRecommend(b *ast.RecommendBlock) *QueryPlan {
	plan := &QueryPlan{BlockName: b.Name}

	// Reference the matched detect/predict result
	if bmc, ok := b.When.(*ast.BlockMatchesCondition); ok {
		plan.Steps = append(plan.Steps, &GoComputation{
			Function: "resolve_block_matches",
			Input:    bmc.Name,
			Params:   map[string]any{"kind": bmc.Kind},
			Into:     "matches",
		})
	}

	// calculate steps
	for _, calc := range b.Calculate {
		plan.Steps = append(plan.Steps, &FactQuery{
			Query:    p.buildCalculateQuery(calc),
			BindVars: map[string]any{},
			Into:     calc.Name,
		})
	}

	if b.Suggest != nil {
		plan.Steps = append(plan.Steps, &GoComputation{
			Function: FuncRenderTemplate,
			Input:    "matches",
			Params:   map[string]any{"template": b.Suggest.Raw},
			Into:     "suggestions",
		})
	}
	return plan
}

func (p *planner) planPredictBlock(b *ast.PredictBlock) *QueryPlan {
	plan := &QueryPlan{BlockName: b.Name}
	qb := p.newQueryBuilder()
	qb.addSelector(b.Selector)
	plan.Steps = append(plan.Steps, &FactQuery{
		Query:    qb.build(),
		BindVars: qb.bindVars(),
		Into:     "candidates",
	})
	plan.Steps = append(plan.Steps, &MLComputation{
		Function: FuncPredictDecisionTree,
		Input:    "candidates",
		Params:   map[string]any{"features": b.Features, "trained_on": b.TrainedOn},
		Into:     "predictions",
	})
	if b.Label != nil {
		plan.Steps = append(plan.Steps, &GoComputation{
			Function: FuncRenderTemplate,
			Input:    "predictions",
			Params:   map[string]any{"template": b.Label.Raw},
			Into:     "results",
		})
	}
	return plan
}

func (p *planner) planForecastBlock(b *ast.ForecastBlock) *QueryPlan {
	plan := &QueryPlan{BlockName: b.Name}
	qb := p.newQueryBuilder()
	qb.addSelector(b.Selector)
	plan.Steps = append(plan.Steps, &FactQuery{
		Query:    qb.build(),
		BindVars: qb.bindVars(),
		Into:     "candidates",
	})
	plan.Steps = append(plan.Steps, &MLComputation{
		Function: FuncForecastExpSmoothing,
		Input:    "candidates",
		Params:   map[string]any{"series": b.Series},
		Into:     "forecasts",
	})
	if b.Label != nil {
		plan.Steps = append(plan.Steps, &GoComputation{
			Function: FuncRenderTemplate,
			Input:    "forecasts",
			Params:   map[string]any{"template": b.Label.Raw},
			Into:     "results",
		})
	}
	return plan
}

func (p *planner) planClusterBlock(b *ast.ClusterBlock) *QueryPlan {
	plan := &QueryPlan{BlockName: b.Name}
	qb := p.newQueryBuilder()
	qb.addSelector(b.Selector)
	plan.Steps = append(plan.Steps, &FactQuery{
		Query:    qb.build(),
		BindVars: qb.bindVars(),
		Into:     "candidates",
	})
	plan.Steps = append(plan.Steps, &MLComputation{
		Function: FuncClusterDBSCAN,
		Input:    "candidates",
		Params:   map[string]any{"by": b.ByAttrs},
		Into:     "clusters",
	})
	return plan
}

func (p *planner) planClassifyBlock(b *ast.ClassifyBlock) *QueryPlan {
	plan := &QueryPlan{BlockName: b.Name}
	qb := p.newQueryBuilder()
	qb.addSelector(b.Selector)
	plan.Steps = append(plan.Steps, &FactQuery{
		Query:    qb.build(),
		BindVars: qb.bindVars(),
		Into:     "candidates",
	})
	plan.Steps = append(plan.Steps, &MLComputation{
		Function: FuncClassifyKNN,
		Input:    "candidates",
		Params:   map[string]any{"features": b.Features},
		Into:     "classifications",
	})
	return plan
}

func (p *planner) planSimilarBlock(b *ast.SimilarBlock) *QueryPlan {
	plan := &QueryPlan{BlockName: b.Name}
	qb := p.newQueryBuilder()
	qb.addSelector(b.Selector)
	plan.Steps = append(plan.Steps, &FactQuery{
		Query:    qb.build(),
		BindVars: qb.bindVars(),
		Into:     "candidates",
	})
	plan.Steps = append(plan.Steps, &MLComputation{
		Function: FuncSimilarityCosine,
		Input:    "candidates",
		Params:   map[string]any{"to": b.To, "within": b.Within},
		Into:     "similar_records",
	})
	return plan
}

func (p *planner) planRelatedBlock(b *ast.RelatedBlock) *QueryPlan {
	plan := &QueryPlan{BlockName: b.Name}
	qb := p.newQueryBuilder()
	qb.addSelector(b.Selector)
	plan.Steps = append(plan.Steps, &FactQuery{
		Query:    qb.build(),
		BindVars: qb.bindVars(),
		Into:     "candidates",
	})
	plan.Steps = append(plan.Steps, &GraphSnapshot{
		CacheKey: b.Name,
		Options:  map[string]any{},
		Into:     "graph",
	})
	plan.Steps = append(plan.Steps, &MLComputation{
		Function: FuncPPRTopK,
		Input:    "candidates",
		Params: map[string]any{
			"graph_var":      "graph",
			"seed_expr":      b.To,
			"seeds_expr":     b.Seeds,
			"top_k":          ptrOrNil(b.TopK),
			"damping":        ptrOrNil(b.Damping),
			"tolerance":      ptrOrNil(b.Tol),
			"max_iterations": ptrOrNil(b.MaxIter),
			"flag_predicate": "topk",
		},
		Into: "related_records",
	})
	if b.Label != nil {
		plan.Steps = append(plan.Steps, &GoComputation{
			Function: FuncRenderTemplate,
			Input:    "related_records",
			Params:   map[string]any{"template": b.Label.Raw},
			Into:     "results",
		})
	}
	return plan
}

// ptrOrNil returns the pointed-to value as any, or nil if the pointer is
// nil. Used to pack optional planner params without nil-typed map entries.
func ptrOrNil[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

func (p *planner) planCombine(b *ast.CombineBlock) *QueryPlan {
	plan := &QueryPlan{BlockName: b.Name}
	qb := p.newQueryBuilder()
	qb.addSelector(b.Selector)

	switch {
	case b.Sequence:
		return p.planCombineACO(plan, qb, b)
	case b.Solver == "linear":
		return p.planCombineILP(plan, qb, b)
	case b.Select != nil:
		return p.planCombineGA(plan, qb, b)
	default:
		return p.planCombinePareto(plan, qb, b)
	}
}

// planCombinePareto emits the v1 plan: bind each objective attr (must be
// *AttrExpr) into the :find clause and call optimize_pareto on the rows.
func (p *planner) planCombinePareto(plan *QueryPlan, qb *queryBuilder, b *ast.CombineBlock) *QueryPlan {
	indices := make([]int, 0, len(b.Optimize))
	for _, oc := range b.Optimize {
		attr, ok := oc.Attr.(*ast.AttrExpr)
		if !ok {
			indices = append(indices, -1)
			continue
		}
		v := qb.varFor(attr.Name)
		qb.addPattern(":attr/"+sanitizeIdent(attr.Name), factstore.Var(v))
		qb.findVars = appendUniq(qb.findVars, v)
		indices = append(indices, indexOf(qb.findVars, v))
	}

	plan.Steps = append(plan.Steps, &FactQuery{
		Query:    qb.build(),
		BindVars: qb.bindVars(),
		Into:     "candidates",
	})
	plan.Steps = append(plan.Steps, &GoComputation{
		Function: FuncOptimizePareto,
		Input:    "candidates",
		Params: map[string]any{
			"objectives":              b.Optimize,
			"return":                  b.Return,
			"block":                   b.Name,
			"find_vars":               qb.findVars,
			"objective_value_indices": indices,
		},
		Into: "frontier",
	})
	return plan
}

// planCombineGA emits the v2 plan: bind every attr referenced by objectives
// and constraints into the Datalog query, then call optimize_ga with the
// subset size and constraint specs. The GA step receives the rows along with
// a name→column-index map so it can evaluate aggregates per candidate subset.
func (p *planner) planCombineGA(plan *QueryPlan, qb *queryBuilder, b *ast.CombineBlock) *QueryPlan {
	attrIndices := map[string]int{}
	bindAttr := func(name string) {
		if _, seen := attrIndices[name]; seen {
			return
		}
		v := qb.varFor(name)
		qb.addPattern(":attr/"+sanitizeIdent(name), factstore.Var(v))
		qb.findVars = appendUniq(qb.findVars, v)
		attrIndices[name] = indexOf(qb.findVars, v)
	}

	for _, oc := range b.Optimize {
		if name, ok := refAttrName(oc.Attr); ok {
			bindAttr(name)
		}
	}
	for _, c := range b.Constraints {
		if name, ok := refAttrName(c.Left); ok {
			bindAttr(name)
		}
	}

	plan.Steps = append(plan.Steps, &FactQuery{
		Query:    qb.build(),
		BindVars: qb.bindVars(),
		Into:     "candidates",
	})

	seed := int64(0)
	if b.Seed != nil {
		seed = *b.Seed
	}

	plan.Steps = append(plan.Steps, &GoComputation{
		Function: FuncOptimizeGA,
		Input:    "candidates",
		Params: map[string]any{
			"objectives":   b.Optimize,
			"constraints":  b.Constraints,
			"select_size":  b.Select.Size,
			"seed":         seed,
			"return":       b.Return,
			"block":        b.Name,
			"find_vars":    qb.findVars,
			"attr_indices": attrIndices,
		},
		Into: "frontier",
	})
	return plan
}

// planCombineACO emits the routing plan: bind the two coordinate attrs into
// the find clause and dispatch to optimize_aco which builds a distance
// matrix and runs ant-colony search.
func (p *planner) planCombineACO(plan *QueryPlan, qb *queryBuilder, b *ast.CombineBlock) *QueryPlan {
	xAttr, _ := b.Coordinates.X.(*ast.AttrExpr)
	yAttr, _ := b.Coordinates.Y.(*ast.AttrExpr)
	attrIndices := map[string]int{}
	if xAttr != nil {
		bindCombineAttr(qb, xAttr.Name, attrIndices)
	}
	if yAttr != nil {
		bindCombineAttr(qb, yAttr.Name, attrIndices)
	}

	plan.Steps = append(plan.Steps, &FactQuery{
		Query:    qb.build(),
		BindVars: qb.bindVars(),
		Into:     "candidates",
	})

	seed := int64(0)
	if b.Seed != nil {
		seed = *b.Seed
	}

	plan.Steps = append(plan.Steps, &GoComputation{
		Function: FuncOptimizeACO,
		Input:    "candidates",
		Params: map[string]any{
			"x_attr":       xAttrName(xAttr),
			"y_attr":       xAttrName(yAttr),
			"seed":         seed,
			"return":       b.Return,
			"block":        b.Name,
			"find_vars":    qb.findVars,
			"attr_indices": attrIndices,
		},
		Into: "tour",
	})
	return plan
}

// planCombineILP emits the exact-solver plan: same Datalevin binding as the
// GA path, but dispatches to optimize_ilp which solves the single-objective
// 0/1 subset problem via branch-and-bound.
func (p *planner) planCombineILP(plan *QueryPlan, qb *queryBuilder, b *ast.CombineBlock) *QueryPlan {
	attrIndices := map[string]int{}
	for _, oc := range b.Optimize {
		if name, ok := refAttrName(oc.Attr); ok {
			bindCombineAttr(qb, name, attrIndices)
		}
	}
	for _, c := range b.Constraints {
		if name, ok := refAttrName(c.Left); ok {
			bindCombineAttr(qb, name, attrIndices)
		}
	}

	plan.Steps = append(plan.Steps, &FactQuery{
		Query:    qb.build(),
		BindVars: qb.bindVars(),
		Into:     "candidates",
	})

	plan.Steps = append(plan.Steps, &GoComputation{
		Function: FuncOptimizeILP,
		Input:    "candidates",
		Params: map[string]any{
			"objectives":   b.Optimize,
			"constraints":  b.Constraints,
			"select_size":  b.Select.Size,
			"return":       b.Return,
			"block":        b.Name,
			"find_vars":    qb.findVars,
			"attr_indices": attrIndices,
		},
		Into: "frontier",
	})
	return plan
}

func bindCombineAttr(qb *queryBuilder, name string, attrIndices map[string]int) {
	if _, seen := attrIndices[name]; seen {
		return
	}
	v := qb.varFor(name)
	qb.addPattern(":attr/"+sanitizeIdent(name), factstore.Var(v))
	qb.findVars = appendUniq(qb.findVars, v)
	attrIndices[name] = indexOf(qb.findVars, v)
}

func xAttrName(a *ast.AttrExpr) string {
	if a == nil {
		return ""
	}
	return a.Name
}

// refAttrName returns the underlying attr name an objective or constraint
// expression references (either a bare *AttrExpr or an *AggregateExpr
// wrapping one). Aggregates of records (e.g. count(records)) return ("", false).
func refAttrName(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.AttrExpr:
		return x.Name, true
	case *ast.AggregateExpr:
		if a, ok := x.Arg.(*ast.AttrExpr); ok {
			return a.Name, true
		}
	}
	return "", false
}

func (p *planner) planWorkflow(b *ast.WorkflowBlock) *QueryPlan {
	plan := &QueryPlan{BlockName: b.Name}
	sorted := topoSortSteps(b.Steps)
	for _, step := range sorted {
		plan.Steps = append(plan.Steps, &GoComputation{
			Function: "mcp_call",
			Input:    "",
			Params: map[string]any{
				"step":       step.Name,
				"depends_on": step.DependsOn,
				"mcp":        step.MCPCall,
			},
			Into: step.Name + "_result",
		})
	}
	return plan
}

// topoSortSteps returns workflow steps in topological order using Kahn's algorithm.
// The validator guarantees no cycles, so this always succeeds.
func topoSortSteps(steps []ast.WorkflowStep) []ast.WorkflowStep {
	byName := map[string]ast.WorkflowStep{}
	inDegree := map[string]int{}
	for _, s := range steps {
		byName[s.Name] = s
		inDegree[s.Name] = 0
	}
	for _, s := range steps {
		for _, dep := range s.DependsOn {
			inDegree[s.Name]++
			_ = dep // dep → s edge
		}
	}

	var queue []string
	for _, s := range steps {
		if inDegree[s.Name] == 0 {
			queue = append(queue, s.Name)
		}
	}

	// Build adjacency: dep → []dependents
	adj := map[string][]string{}
	for _, s := range steps {
		for _, dep := range s.DependsOn {
			adj[dep] = append(adj[dep], s.Name)
		}
	}

	var sorted []ast.WorkflowStep
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		sorted = append(sorted, byName[name])
		for _, next := range adj[name] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	return sorted
}

// ─── Query builder ────────────────────────────────────────────────────────────

type queryBuilder struct {
	defines        map[string]*ast.DefineBlock
	entityVar      string
	findVars       []string
	whereClauses   []factstore.Clause
	goConditions   []ast.Condition
	anomalyConds   []anomalyBinding
	thresholdConds []thresholdBinding
	usedVars       map[string]int // base name → count (for dedup)
}

// pattern is a builder helper for an EAV match clause where the entity is
// bound to the builder's entity variable.
func (b *queryBuilder) pattern(attr string, value factstore.Term) *factstore.Pattern {
	return &factstore.Pattern{
		Entity:    factstore.Term{Var: b.entityVar},
		Attribute: attr,
		Value:     value,
	}
}

// addPattern appends a value-binding pattern. `value` is a variable name
// (will become Term.Var) or a literal Go value (becomes Term.Literal).
func (b *queryBuilder) addPattern(attr string, value factstore.Term) {
	b.whereClauses = append(b.whereClauses, b.pattern(attr, value))
}

// addPredicate appends a comparison or string-match predicate.
func (b *queryBuilder) addPredicate(op string, left, right factstore.Term) {
	b.whereClauses = append(b.whereClauses, &factstore.Predicate{
		Op:    op,
		Left:  left,
		Right: right,
	})
}

// anomalyBinding records an `attr X is anomaly` clause lifted out of the
// Datalog selector into an MLComputation step. The Datalog query is widened
// to also return the bound value var so the primitive can score it.
type anomalyBinding struct {
	AttrPath string       // ":attr/weekly_consumption"
	AttrName string       // "weekly_consumption" — what label templates show
	ValueVar string       // "?weekly_consumption"
	Method   string       // "zscore" (default), "grubbs"
	Window   ast.Duration // 12 weeks, 30 days…
}

// thresholdBinding records an `attr X OP learned_threshold ...` compare clause
// lifted out of the Datalog selector into an MLComputation step.
type thresholdBinding struct {
	AttrPath string
	AttrName string
	ValueVar string
	Method   string       // "p95"
	Op       string       // ">", "<", ">=", "<="
	Window   ast.Duration // recorded for explanation/audit; not enforced in phase 2
}

func (p *planner) newQueryBuilder() *queryBuilder {
	return &queryBuilder{
		defines:   p.defines,
		entityVar: "?e",
		findVars:  []string{"?e"},
		usedVars:  map[string]int{},
	}
}

func (b *queryBuilder) varFor(base string) string {
	base = sanitizeIdent(base)
	if _, seen := b.usedVars[base]; !seen {
		b.usedVars[base] = 0
		return "?" + base
	}
	b.usedVars[base]++
	return fmt.Sprintf("?%s_%d", base, b.usedVars[base])
}

func (b *queryBuilder) bindVars() map[string]any {
	return map[string]any{} // runtime injects entity_id; nothing static here
}

func (b *queryBuilder) addSelector(sel ast.Selector) {
	for _, cond := range sel.Conditions {
		b.addCondition(cond)
	}
}

func (b *queryBuilder) addCondition(cond ast.Condition) {
	if cond == nil {
		return
	}
	switch c := cond.(type) {
	case *ast.LogicalCondition:
		b.addLogical(c)
	case *ast.NotCondition:
		b.addNot(c)
	case *ast.CompareCondition:
		b.addCompare(c)
	case *ast.MembershipCondition:
		b.addMembership(c)
	case *ast.IsCondition:
		b.addIsCondition(c)
	case *ast.StringMatchCondition:
		b.addStringMatch(c)
	case *ast.AnomalyCondition:
		b.addAnomalyCondition(c)
	default:
		// TemporalCondition, ChangedToCondition, HasCondition — cannot express
		// in Datalog, defer to Go.
		b.goConditions = append(b.goConditions, cond)
	}
}

func (b *queryBuilder) addLogical(c *ast.LogicalCondition) {
	if c.Op == "and" {
		b.addCondition(c.Left)
		b.addCondition(c.Right)
		return
	}
	// OR: collect sub-clauses from each side in a nested builder
	left := b.subClauses(c.Left)
	right := b.subClauses(c.Right)
	b.whereClauses = append(b.whereClauses, &factstore.Or{
		Branches: [][]factstore.Clause{left, right},
	})
}

func (b *queryBuilder) addNot(c *ast.NotCondition) {
	sub := b.subClauses(c.Inner)
	b.whereClauses = append(b.whereClauses, &factstore.Not{Body: sub})
}

func (b *queryBuilder) subClauses(cond ast.Condition) []factstore.Clause {
	child := &queryBuilder{
		defines:   b.defines,
		entityVar: b.entityVar,
		usedVars:  copyMap(b.usedVars),
	}
	child.addCondition(cond)
	// merge any go conditions back up
	b.goConditions = append(b.goConditions, child.goConditions...)
	return child.whereClauses
}

func (b *queryBuilder) addCompare(c *ast.CompareCondition) {
	if lt, ok := c.Right.(*ast.LearnedThresholdExpr); ok {
		b.addLearnedThresholdCompare(c, lt)
		return
	}

	leftPath, leftIsField := exprToFieldPath(c.Left)
	rightPath, rightIsField := exprToFieldPath(c.Right)
	leftLitVal, leftIsLit := exprToGoLiteral(c.Left)
	rightLitVal, rightIsLit := exprToGoLiteral(c.Right)

	switch {
	case leftIsField && rightIsLit:
		if c.Op == "==" {
			// Direct value binding: pattern matches the literal value.
			b.addPattern(leftPath, factstore.Lit(rightLitVal))
		} else {
			// Bind to a variable, then constrain via predicate.
			v := b.varFor(attrVarName(c.Left))
			b.addPattern(leftPath, factstore.Var(v))
			b.findVars = appendUniq(b.findVars, v)
			b.addPredicate(c.Op, factstore.Var(v), factstore.Lit(rightLitVal))
		}

	case leftIsField && rightIsField:
		// attr1 OP attr2
		lv := b.varFor(attrVarName(c.Left))
		rv := b.varFor(attrVarName(c.Right))
		b.addPattern(leftPath, factstore.Var(lv))
		b.addPattern(rightPath, factstore.Var(rv))
		b.findVars = appendUniq(b.findVars, lv, rv)
		b.addPredicate(c.Op, factstore.Var(lv), factstore.Var(rv))

	case leftIsLit && rightIsField:
		// literal OP attr (reversed) — bind the attr, predicate on the literal.
		rv := b.varFor(attrVarName(c.Right))
		b.addPattern(rightPath, factstore.Var(rv))
		b.findVars = appendUniq(b.findVars, rv)
		b.addPredicate(c.Op, factstore.Lit(leftLitVal), factstore.Var(rv))

	default:
		// Complex expr (binary arithmetic, context refs, etc.) → Go
		b.goConditions = append(b.goConditions, c)
	}
}

func (b *queryBuilder) addMembership(c *ast.MembershipCondition) {
	path, ok := exprToFieldPath(c.Expr)
	if !ok {
		b.goConditions = append(b.goConditions, c)
		return
	}
	// Check if members are category_tree expression
	if len(c.Members) == 1 {
		if _, isCT := c.Members[0].(*ast.CategoryTreeExpr); isCT {
			// recursive tree — GoComputation
			b.goConditions = append(b.goConditions, c)
			return
		}
	}
	v := b.varFor(attrVarName(c.Expr))
	b.addPattern(path, factstore.Var(v))
	b.findVars = appendUniq(b.findVars, v)

	members := make([]any, 0, len(c.Members))
	for _, m := range c.Members {
		if val, ok := exprToGoLiteral(m); ok {
			members = append(members, val)
		}
	}
	op := "in"
	if c.Negated {
		op = "not_in"
	}
	b.addPredicate(op, factstore.Var(v), factstore.Lit(members))
}

func (b *queryBuilder) addIsCondition(c *ast.IsCondition) {
	// inline the define's conditions
	if def, ok := b.defines[c.Name]; ok {
		for _, dc := range def.Conditions {
			b.addCondition(dc)
		}
	}
	// if no define found, validator already reported the error
}

// addLearnedThresholdCompare lifts `attr X OP learned_threshold ...` out of
// the selector. The query is widened to return the bound value var so the
// primitive can compute the percentile and per-row decision.
func (b *queryBuilder) addLearnedThresholdCompare(c *ast.CompareCondition, lt *ast.LearnedThresholdExpr) {
	path, ok := exprToFieldPath(c.Left)
	if !ok {
		b.goConditions = append(b.goConditions, c)
		return
	}
	attrName := attrVarName(c.Left)
	v := b.varFor(attrName)
	b.addPattern(path, factstore.Var(v))
	b.findVars = appendUniq(b.findVars, v)
	b.thresholdConds = append(b.thresholdConds, thresholdBinding{
		AttrPath: path,
		AttrName: attrName,
		ValueVar: v,
		Method:   lt.Method,
		Op:       c.Op,
		Window:   lt.Window,
	})
}

// addAnomalyCondition lifts `attr X is anomaly` out of the selector: it
// binds the value var into the :find clause so the resulting rows carry
// the numeric series, and records a binding the planner uses to emit an
// MLComputation step after the query.
func (b *queryBuilder) addAnomalyCondition(c *ast.AnomalyCondition) {
	path, ok := exprToFieldPath(c.Subject)
	if !ok {
		b.goConditions = append(b.goConditions, c)
		return
	}
	attrName := attrVarName(c.Subject)
	v := b.varFor(attrName)
	b.addPattern(path, factstore.Var(v))
	b.findVars = appendUniq(b.findVars, v)
	b.anomalyConds = append(b.anomalyConds, anomalyBinding{
		AttrPath: path,
		AttrName: attrName,
		ValueVar: v,
		Method:   c.Method,
		Window:   c.Window,
	})
}

func (b *queryBuilder) addStringMatch(c *ast.StringMatchCondition) {
	path, ok := exprToFieldPath(c.Subject)
	if !ok {
		b.goConditions = append(b.goConditions, c)
		return
	}
	v := b.varFor(attrVarName(c.Subject))
	b.addPattern(path, factstore.Var(v))
	b.findVars = appendUniq(b.findVars, v)
	b.addPredicate(c.Op, factstore.Var(v), factstore.Lit(c.Value))
}

// build emits the structured Query the planner attaches to each FactQuery
// plan step. Backends never see Datalog text — the Datalevin client owns
// the only translator that turns this back into wire-format Datalog.
func (b *queryBuilder) build() factstore.Query {
	return factstore.Query{
		Find:  append([]string(nil), b.findVars...),
		Where: append([]factstore.Clause(nil), b.whereClauses...),
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// exprToFieldPath maps a Talon expression to a Datalevin attribute path.
func exprToFieldPath(e ast.Expr) (path string, ok bool) {
	switch v := e.(type) {
	case *ast.IdentExpr:
		switch v.Name {
		case "type":
			return ":record/type", true
		case "status":
			return ":record/status", true
		case "category":
			return ":record/category", true
		default:
			return ":record/" + sanitizeIdent(v.Name), true
		}
	case *ast.AttrExpr:
		return ":attr/" + sanitizeIdent(v.Name), true
	}
	return "", false
}

// exprToGoLiteral extracts the underlying Go value from a literal AST node.
// Returns (value, true) when the expression is a literal; (nil, false)
// otherwise. The structured Query model carries Go values directly, so the
// translator (Datalevin) or interpreter (MemoryStore) decides on rendering.
func exprToGoLiteral(e ast.Expr) (any, bool) {
	lit, ok := e.(*ast.LiteralExpr)
	if !ok {
		return nil, false
	}
	return lit.Value, lit.Value != nil
}

// attrVarName returns a Datalevin variable base name for an expression.
func attrVarName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.AttrExpr:
		return v.Name
	case *ast.IdentExpr:
		return v.Name
	}
	return "val"
}

func sanitizeIdent(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, " ", "_"), "-", "_")
}

func renderGoConditions(conds []ast.Condition) string {
	parts := make([]string, 0, len(conds))
	for _, c := range conds {
		parts = append(parts, fmt.Sprintf("/* %T */", c))
	}
	return strings.Join(parts, " && ")
}

func (p *planner) buildCalculateQuery(calc ast.CalculateClause) factstore.Query {
	qb := p.newQueryBuilder()
	for _, c := range calc.Where {
		qb.addCondition(c)
	}
	return qb.build()
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func appendUniq(s []string, vs ...string) []string {
	seen := map[string]bool{}
	for _, x := range s {
		seen[x] = true
	}
	for _, v := range vs {
		if !seen[v] {
			s = append(s, v)
			seen[v] = true
		}
	}
	return s
}

func copyMap(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

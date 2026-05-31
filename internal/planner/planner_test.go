package planner

import (
	"strings"
	"testing"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/lexer"
	"github.com/opentalon/talon-language/internal/parser"
	"github.com/opentalon/talon-language/internal/validator"
)

func planAll(t *testing.T, src string) map[string]*QueryPlan {
	t.Helper()
	tokens, ld := lexer.Lex("test.talon", src)
	if ld.HasErrors() {
		t.Fatalf("lex: %v", ld)
	}
	prog, pd := parser.Parse("test.talon", tokens)
	if pd.HasErrors() {
		t.Fatalf("parse: %v", pd)
	}
	vd := validator.Validate("test.talon", prog)
	if vd.HasErrors() {
		t.Fatalf("validate: %v", vd)
	}
	plans, diags := Plan(prog)
	if diags.HasErrors() {
		t.Fatalf("plan: %v", diags)
	}
	return plans
}

func planBlock(t *testing.T, src string, name string) *QueryPlan {
	t.Helper()
	plans := planAll(t, src)
	p, ok := plans[name]
	if !ok {
		t.Fatalf("no plan for block %q, got: %v", name, keys(plans))
	}
	return p
}

func keys(m map[string]*QueryPlan) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func queryStep(t *testing.T, plan *QueryPlan, i int) *DatalevinQuery {
	t.Helper()
	if i >= len(plan.Steps) {
		t.Fatalf("step[%d]: out of range (len=%d)", i, len(plan.Steps))
	}
	q, ok := plan.Steps[i].(*DatalevinQuery)
	if !ok {
		t.Fatalf("step[%d]: expected *DatalevinQuery, got %T", i, plan.Steps[i])
	}
	return q
}

func mlStep(t *testing.T, plan *QueryPlan, i int) *MLComputation {
	t.Helper()
	if i >= len(plan.Steps) {
		t.Fatalf("step[%d]: out of range (len=%d)", i, len(plan.Steps))
	}
	m, ok := plan.Steps[i].(*MLComputation)
	if !ok {
		t.Fatalf("step[%d]: expected *MLComputation, got %T", i, plan.Steps[i])
	}
	return m
}

func findMLStep(plan *QueryPlan, fn string) *MLComputation {
	for _, s := range plan.Steps {
		if m, ok := s.(*MLComputation); ok && m.Function == fn {
			return m
		}
	}
	return nil
}

func assertContains(t *testing.T, query, fragment string) {
	t.Helper()
	if !strings.Contains(query, fragment) {
		t.Errorf("query does not contain %q\ngot:\n%s", fragment, query)
	}
}

// ─── Defines produce no plan ───────────────────────────────────────────────────

func TestPlanDefineProducesNoPlan(t *testing.T) {
	plans := planAll(t, `
define "high_value" {
  attr "price" > 10000
}`)
	if _, ok := plans["high_value"]; ok {
		t.Error("define block should not produce a plan")
	}
}

// ─── detect ────────────────────────────────────────────────────────────────────

func TestPlanDetectSimpleQuery(t *testing.T) {
	plan := planBlock(t, `
detect "Low stock" {
  for records where type == "stock_item"
  flag matching items
}`, "Low stock")

	if len(plan.Steps) == 0 {
		t.Fatal("expected at least one step")
	}
	q := queryStep(t, plan, 0)
	assertContains(t, q.Query, ":find")
	assertContains(t, q.Query, ":where")
	assertContains(t, q.Query, `:record/type`)
	assertContains(t, q.Query, `"stock_item"`)
}

func TestPlanDetectAttrComparison(t *testing.T) {
	plan := planBlock(t, `
detect "Overdue km" {
  for records where attr "km" > 20000
  flag matching items
}`, "Overdue km")

	q := queryStep(t, plan, 0)
	assertContains(t, q.Query, ":attr/km")
	assertContains(t, q.Query, "?km")
	assertContains(t, q.Query, "> ?km 20000")
}

func TestPlanDetectMultiConditionAnd(t *testing.T) {
	plan := planBlock(t, `
detect "Active items" {
  for records where type == "item"
    and status == "active"
    and attr "price" > 100
  flag matching items
}`, "Active items")

	q := queryStep(t, plan, 0)
	assertContains(t, q.Query, `:record/type`)
	assertContains(t, q.Query, `:record/status`)
	assertContains(t, q.Query, `:attr/price`)
}

func TestPlanDetectStatusNotEqual(t *testing.T) {
	plan := planBlock(t, `
detect "Not archived" {
  for records where status != "archived"
  flag matching items
}`, "Not archived")

	q := queryStep(t, plan, 0)
	assertContains(t, q.Query, `not=`)
	assertContains(t, q.Query, `"archived"`)
}

func TestPlanDetectMembership(t *testing.T) {
	plan := planBlock(t, `
detect "In category" {
  for records where status in ["active", "pending"]
  flag matching items
}`, "In category")

	q := queryStep(t, plan, 0)
	assertContains(t, q.Query, `contains?`)
	assertContains(t, q.Query, `"active"`)
	assertContains(t, q.Query, `"pending"`)
}

func TestPlanDetectOrCondition(t *testing.T) {
	plan := planBlock(t, `
detect "Either" {
  for records where type == "item" or type == "equipment"
  flag matching items
}`, "Either")

	q := queryStep(t, plan, 0)
	assertContains(t, q.Query, "(or")
}

func TestPlanDetectNotCondition(t *testing.T) {
	plan := planBlock(t, `
detect "Not inactive" {
  for records where not status == "inactive"
  flag matching items
}`, "Not inactive")

	q := queryStep(t, plan, 0)
	assertContains(t, q.Query, "(not")
}

func TestPlanDetectAnomalyConditionEmitsMLStep(t *testing.T) {
	plan := planBlock(t, `
detect "Unusual" {
  for records where type == "stock_item"
    and attr "weekly_consumption" is anomaly compared_to last 12 weeks
  flag matching items
}`, "Unusual")

	q := queryStep(t, plan, 0)
	// Query must bind the value var so the primitive can score it.
	assertContains(t, q.Query, ":attr/weekly_consumption")
	assertContains(t, q.Query, "?weekly_consumption")
	assertContains(t, q.Query, ":find ?e ?weekly_consumption")

	m := findMLStep(plan, FuncAnomalyZscore)
	if m == nil {
		t.Fatal("expected MLComputation with anomaly_zscore")
	}
	if attr, _ := m.Params["attr"].(string); attr != "weekly_consumption" {
		t.Errorf("Params[attr]: got %q, want %q", attr, "weekly_consumption")
	}
	if idx, _ := m.Params["value_index"].(int); idx != 1 {
		t.Errorf("Params[value_index]: got %d, want 1", idx)
	}
}

func TestPlanDetectLearnedThresholdEmitsMLStep(t *testing.T) {
	plan := planBlock(t, `
detect "High mileage" {
  for records where type == "item"
    and attr "km" > learned_threshold p95 of attr "km" over last 90 days
  flag matching items
}`, "High mileage")

	q := queryStep(t, plan, 0)
	assertContains(t, q.Query, ":attr/km")
	assertContains(t, q.Query, "?km")
	assertContains(t, q.Query, ":find ?e ?km")

	m := findMLStep(plan, FuncLearnedThreshold)
	if m == nil {
		t.Fatal("expected MLComputation with learned_threshold")
	}
	if attr, _ := m.Params["attr"].(string); attr != "km" {
		t.Errorf("Params[attr]: got %q, want %q", attr, "km")
	}
	if method, _ := m.Params["method"].(string); method != "p95" {
		t.Errorf("Params[method]: got %q, want %q", method, "p95")
	}
	if op, _ := m.Params["op"].(string); op != ">" {
		t.Errorf("Params[op]: got %q, want %q", op, ">")
	}
	if idx, _ := m.Params["value_index"].(int); idx != 1 {
		t.Errorf("Params[value_index]: got %d, want 1", idx)
	}
}

func TestPlanDetectWithAnomalyMLStep(t *testing.T) {
	plan := planBlock(t, `
detect "Unusual" {
  for records where type == "stock_item"
  is anomaly compared_to last 12 weeks
  flag matching items
}`, "Unusual")

	if len(plan.Steps) < 2 {
		t.Fatalf("expected DatalevinQuery + MLComputation, got %d steps", len(plan.Steps))
	}
	queryStep(t, plan, 0)
	m := mlStep(t, plan, 1)
	if m.Function != FuncAnomalyZscore {
		t.Errorf("MLComputation.Function: got %q, want %q", m.Function, FuncAnomalyZscore)
	}
}

func TestPlanDetectWithLabel(t *testing.T) {
	plan := planBlock(t, `
detect "Low stock" {
  for records where type == "item"
  flag matching items
  label "{item.name}: low"
}`, "Low stock")

	// Last step should be template rendering
	last := plan.Steps[len(plan.Steps)-1]
	g, ok := last.(*GoComputation)
	if !ok {
		t.Fatalf("last step: expected GoComputation, got %T", last)
	}
	if g.Function != FuncRenderTemplate {
		t.Errorf("last step function: got %q, want %q", g.Function, FuncRenderTemplate)
	}
}

// ─── Define inlining ──────────────────────────────────────────────────────────

func TestPlanDefineInlining(t *testing.T) {
	plan := planBlock(t, `
define "high_value" {
  attr "price" > 10000
}
detect "Expensive" {
  for records where is "high_value"
  flag matching items
}`, "Expensive")

	q := queryStep(t, plan, 0)
	// define's condition should be inlined: attr "price" > 10000
	assertContains(t, q.Query, ":attr/price")
	assertContains(t, q.Query, "10000")
}

// ─── rule ──────────────────────────────────────────────────────────────────────

func TestPlanRule(t *testing.T) {
	plan := planBlock(t, `
rule "No assign" {
  for records where type == "item"
    and status == "active"
  block "assign"
}`, "No assign")

	if len(plan.Steps) == 0 {
		t.Fatal("expected steps")
	}
	queryStep(t, plan, 0)
}

// ─── recommend ─────────────────────────────────────────────────────────────────

func TestPlanRecommend(t *testing.T) {
	plan := planBlock(t, `
detect "Low stock" {
  for records where type == "item"
  flag matching items
}
recommend "Order more" {
  when detect "Low stock" matches
  suggest "Place order"
}`, "Order more")

	// recommend: step referencing the detect result + suggest render
	if len(plan.Steps) == 0 {
		t.Fatal("expected steps")
	}
}

// ─── top-level ML blocks ───────────────────────────────────────────────────────

func TestPlanPredictBlock(t *testing.T) {
	plan := planBlock(t, `
predict "Failure risk" {
  for records where type == "item"
  features [attr "km", attr "age"]
  label "Risk"
}`, "Failure risk")

	queryStep(t, plan, 0)
	if findMLStep(plan, FuncPredictDecisionTree) == nil {
		t.Error("expected MLComputation with predict_decision_tree")
	}
}

func TestPlanForecastBlock(t *testing.T) {
	plan := planBlock(t, `
forecast "Stock out" {
  for records where type == "item"
  series attr "stock" over last 30 days
  label "Out soon"
}`, "Stock out")

	queryStep(t, plan, 0)
	if findMLStep(plan, FuncForecastExpSmoothing) == nil {
		t.Error("expected MLComputation with forecast_exponential_smoothing")
	}
}

func TestPlanClusterBlock(t *testing.T) {
	plan := planBlock(t, `
cluster "Segments" {
  for records where type == "item"
  by [attr "km", attr "age"]
}`, "Segments")

	queryStep(t, plan, 0)
	m := findMLStep(plan, FuncClusterDBSCAN)
	if m == nil {
		t.Fatal("expected MLComputation with cluster_dbscan")
	}
	if _, ok := m.Params["by"]; !ok {
		t.Error("MLComputation.Params missing 'by'")
	}
}

func TestPlanClassifyBlock(t *testing.T) {
	plan := planBlock(t, `
classify "Tickets" {
  for records where type == "ticket"
  features [attr "title", attr "body"]
}`, "Tickets")

	queryStep(t, plan, 0)
	if findMLStep(plan, FuncClassifyKNN) == nil {
		t.Error("expected MLComputation with classify_knn")
	}
}

func TestPlanSimilarBlock(t *testing.T) {
	plan := planBlock(t, `
find similar "Neighbors" {
  for records where type == "item"
  to attr "title"
  within 5
}`, "Neighbors")

	queryStep(t, plan, 0)
	if findMLStep(plan, FuncSimilarityCosine) == nil {
		t.Error("expected MLComputation with similarity_cosine")
	}
}

func TestIsMLFunction(t *testing.T) {
	mlFns := []string{
		FuncAnomalyZscore, FuncPredictDecisionTree, FuncForecastExpSmoothing,
		FuncClusterDBSCAN, FuncSimilarityCosine, FuncClassifyKNN,
	}
	for _, fn := range mlFns {
		if !IsMLFunction(fn) {
			t.Errorf("IsMLFunction(%q) = false, want true", fn)
		}
	}
	nonML := []string{FuncRenderTemplate, "resolve_block_matches", "mcp_call", "optimize_min"}
	for _, fn := range nonML {
		if IsMLFunction(fn) {
			t.Errorf("IsMLFunction(%q) = true, want false", fn)
		}
	}
}

// ─── EmitDatalevin ─────────────────────────────────────────────────────────────

func TestEmitDatalevin(t *testing.T) {
	q := &DatalevinQuery{
		Query: "[:find ?e :where [?e :record/type \"item\"]]",
		Into:  "results",
	}
	got := EmitDatalevin(q)
	if got != q.Query {
		t.Errorf("EmitDatalevin: got %q, want %q", got, q.Query)
	}
}

// ─── Multiple blocks ───────────────────────────────────────────────────────────

func TestPlanMultipleBlocks(t *testing.T) {
	plans := planAll(t, `
detect "D1" {
  for records where type == "item"
  flag matching items
}
rule "R1" {
  for records where status == "pending"
  block "action"
}
define "helper" {
  attr "price" > 100
}`)

	if _, ok := plans["D1"]; !ok {
		t.Error("missing plan for D1")
	}
	if _, ok := plans["R1"]; !ok {
		t.Error("missing plan for R1")
	}
	if _, ok := plans["helper"]; ok {
		t.Error("define should not have a plan")
	}
}

// ─── Workflow planning ───────────────────────────────────────────────────────

func goStep(t *testing.T, plan *QueryPlan, i int) *GoComputation {
	t.Helper()
	if i >= len(plan.Steps) {
		t.Fatalf("step[%d]: out of range (len=%d)", i, len(plan.Steps))
	}
	gc, ok := plan.Steps[i].(*GoComputation)
	if !ok {
		t.Fatalf("step[%d]: expected *GoComputation, got %T", i, plan.Steps[i])
	}
	return gc
}

func TestPlanWorkflowTopoSort(t *testing.T) {
	plan := planBlock(t, `
workflow "Deploy" {
  step "notify" depends_on "deploy" {
    mcp "slack" "post" { channel "ops" }
  }
  step "deploy" depends_on "build" {
    mcp "ci" "deploy" { env "prod" }
  }
  step "build" {
    mcp "ci" "build" { branch "main" }
  }
}`, "Deploy")

	if len(plan.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(plan.Steps))
	}

	// Topological order: build → deploy → notify
	s0 := goStep(t, plan, 0)
	s1 := goStep(t, plan, 1)
	s2 := goStep(t, plan, 2)
	if s0.Params["step"] != "build" {
		t.Errorf("step[0]: expected build, got %v", s0.Params["step"])
	}
	if s1.Params["step"] != "deploy" {
		t.Errorf("step[1]: expected deploy, got %v", s1.Params["step"])
	}
	if s2.Params["step"] != "notify" {
		t.Errorf("step[2]: expected notify, got %v", s2.Params["step"])
	}
}

// ─── Combine / Pareto planning ───────────────────────────────────────────────

func TestPlanCombine_ParetoEmitsTwoSteps(t *testing.T) {
	plan := planBlock(t, `
combine "Dispatch picks" {
  for records where type == "item" and status == "active"
  minimize attr "cost_per_km"
  maximize attr "urgency_score"
  return id, cost_per_km, urgency_score
}`, "Dispatch picks")

	if len(plan.Steps) != 2 {
		t.Fatalf("expected 2 steps (DatalevinQuery + GoComputation), got %d", len(plan.Steps))
	}

	q := queryStep(t, plan, 0)
	// The objective attrs must be bound into the find clause so rows
	// carry the per-objective value the optimizer reads.
	assertContains(t, q.Query, "?cost_per_km")
	assertContains(t, q.Query, "?urgency_score")
	assertContains(t, q.Query, ":attr/cost_per_km")
	assertContains(t, q.Query, ":attr/urgency_score")

	gc := goStep(t, plan, 1)
	if gc.Function != FuncOptimizePareto {
		t.Errorf("step[1].Function = %q, want %q", gc.Function, FuncOptimizePareto)
	}
	objs, ok := gc.Params["objectives"].([]ast.OptimizeClause)
	if !ok {
		t.Fatalf("objectives param: want []ast.OptimizeClause, got %T", gc.Params["objectives"])
	}
	if len(objs) != 2 {
		t.Fatalf("objectives: want 2, got %d", len(objs))
	}
	if objs[0].Direction != "minimize" || objs[1].Direction != "maximize" {
		t.Errorf("directions: want [minimize maximize], got [%s %s]", objs[0].Direction, objs[1].Direction)
	}
	indices, ok := gc.Params["objective_value_indices"].([]int)
	if !ok {
		t.Fatalf("objective_value_indices param: want []int, got %T", gc.Params["objective_value_indices"])
	}
	if len(indices) != 2 {
		t.Fatalf("indices: want 2, got %v", indices)
	}
	for i, idx := range indices {
		if idx <= 0 {
			t.Errorf("indices[%d] = %d, want positive column (?e is at 0)", i, idx)
		}
	}
}

func TestPlanCombine_SingleObjectiveStillWorks(t *testing.T) {
	plan := planBlock(t, `
combine "Cheapest" {
  for records where type == "item"
  minimize attr "cost_per_km"
  return id
}`, "Cheapest")

	gc := goStep(t, plan, 1)
	if gc.Function != FuncOptimizePareto {
		t.Errorf("Function = %q, want %q", gc.Function, FuncOptimizePareto)
	}
	objs := gc.Params["objectives"].([]ast.OptimizeClause)
	if len(objs) != 1 {
		t.Fatalf("objectives: want 1, got %d", len(objs))
	}
}

func TestPlanCombine_GASubsetMode(t *testing.T) {
	plan := planBlock(t, `
combine "Reorder picks" {
  for records where type == "stock_item" and status == "active"
  select 3 from records
  minimize total(attr "cost")
  maximize total(attr "urgency")
  subject_to total(attr "cost") <= 5000
  return id
  seed 42
}`, "Reorder picks")

	if len(plan.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(plan.Steps))
	}

	q := queryStep(t, plan, 0)
	assertContains(t, q.Query, ":attr/cost")
	assertContains(t, q.Query, ":attr/urgency")

	gc := goStep(t, plan, 1)
	if gc.Function != FuncOptimizeGA {
		t.Errorf("Function: got %q, want %q", gc.Function, FuncOptimizeGA)
	}
	size, _ := gc.Params["select_size"].(int)
	if size != 3 {
		t.Errorf("select_size: got %d, want 3", size)
	}
	seed, _ := gc.Params["seed"].(int64)
	if seed != 42 {
		t.Errorf("seed: got %d, want 42", seed)
	}
	cons, _ := gc.Params["constraints"].([]ast.ConstraintClause)
	if len(cons) != 1 {
		t.Fatalf("constraints: want 1, got %d", len(cons))
	}
	if cons[0].Op != "<=" {
		t.Errorf("constraint op: got %q, want <=", cons[0].Op)
	}
	attrIdx, _ := gc.Params["attr_indices"].(map[string]int)
	if _, ok := attrIdx["cost"]; !ok {
		t.Errorf("attr_indices missing cost: %v", attrIdx)
	}
	if _, ok := attrIdx["urgency"]; !ok {
		t.Errorf("attr_indices missing urgency: %v", attrIdx)
	}
}

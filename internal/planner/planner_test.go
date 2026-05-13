package planner

import (
	"strings"
	"testing"

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

func goStep(t *testing.T, plan *QueryPlan, i int) *GoComputation {
	t.Helper()
	if i >= len(plan.Steps) {
		t.Fatalf("step[%d]: out of range (len=%d)", i, len(plan.Steps))
	}
	g, ok := plan.Steps[i].(*GoComputation)
	if !ok {
		t.Fatalf("step[%d]: expected *GoComputation, got %T", i, plan.Steps[i])
	}
	return g
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

func TestPlanDetectWithAnomalyGoStep(t *testing.T) {
	plan := planBlock(t, `
detect "Unusual" {
  for records where type == "stock_item"
  is anomaly compared_to last 12 weeks
  flag matching items
}`, "Unusual")

	if len(plan.Steps) < 2 {
		t.Fatalf("expected DatalevinQuery + GoComputation, got %d steps", len(plan.Steps))
	}
	queryStep(t, plan, 0)
	g := goStep(t, plan, 1)
	if g.Function != FuncAnomalyZscore {
		t.Errorf("GoComputation.Function: got %q, want %q", g.Function, FuncAnomalyZscore)
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
	// Should have GoComputation for predict
	var foundPredict bool
	for _, s := range plan.Steps {
		if g, ok := s.(*GoComputation); ok && g.Function == FuncPredictDecisionTree {
			foundPredict = true
		}
	}
	if !foundPredict {
		t.Error("expected GoComputation with predict_decision_tree")
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
	var foundForecast bool
	for _, s := range plan.Steps {
		if g, ok := s.(*GoComputation); ok && g.Function == FuncForecastExpSmoothing {
			foundForecast = true
		}
	}
	if !foundForecast {
		t.Error("expected GoComputation with forecast_exponential_smoothing")
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

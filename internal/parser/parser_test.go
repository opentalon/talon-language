package parser

import (
	"testing"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/lexer"
)

func mustParse(t *testing.T, src string) *ast.Program {
	t.Helper()
	tokens, ld := lexer.Lex("test.talon", src)
	if ld.HasErrors() {
		t.Fatalf("lex errors: %v", ld)
	}
	prog, pd := Parse("test.talon", tokens)
	if pd.HasErrors() {
		t.Fatalf("parse errors: %v", pd)
	}
	return prog
}

func block[T ast.Block](t *testing.T, prog *ast.Program, i int) T {
	t.Helper()
	if i >= len(prog.Blocks) {
		t.Fatalf("block[%d]: out of range (len=%d)", i, len(prog.Blocks))
	}
	b, ok := prog.Blocks[i].(T)
	if !ok {
		t.Fatalf("block[%d]: wrong type, got %T", i, prog.Blocks[i])
	}
	return b
}

// ─── Empty ─────────────────────────────────────────────────────────────────────

func TestParseEmpty(t *testing.T) {
	prog := mustParse(t, "")
	if len(prog.Blocks) != 0 {
		t.Fatalf("empty source: expected 0 blocks, got %d", len(prog.Blocks))
	}
}

// ─── detect ────────────────────────────────────────────────────────────────────

func TestParseDetectMinimal(t *testing.T) {
	prog := mustParse(t, `
detect "Cement running low" {
  for records where type == "stock_item"
  flag matching items
  label "{item.name}: low"
  priority CRITICAL
}`)
	b := block[*ast.DetectBlock](t, prog, 0)
	if b.Name != "Cement running low" {
		t.Errorf("Name: got %q", b.Name)
	}
	if b.Flag == nil || b.Flag.Kind != "items" {
		t.Errorf("Flag: got %v", b.Flag)
	}
	if b.Label == nil || b.Label.Raw != "{item.name}: low" {
		t.Errorf("Label: got %v", b.Label)
	}
	if b.Priority == nil || *b.Priority != ast.PriorityCritical {
		t.Errorf("Priority: got %v", b.Priority)
	}
}

func TestParseDetectSelectorConditions(t *testing.T) {
	prog := mustParse(t, `
detect "Multi-cond" {
  for records where type == "item"
    and status == "active"
    and attr "km" > 20000
  flag matching items
}`)
	b := block[*ast.DetectBlock](t, prog, 0)
	// Selector should have a LogicalCondition tree
	if b.Selector.Target != "records" {
		t.Errorf("Selector.Target: got %q", b.Selector.Target)
	}
	if len(b.Selector.Conditions) == 0 {
		t.Error("Selector.Conditions: empty")
	}
}

func TestParseDetectConfidence(t *testing.T) {
	prog := mustParse(t, `
detect "High confidence" {
  for records where type == "item"
  confidence >= 0.9
  flag matching items
}`)
	b := block[*ast.DetectBlock](t, prog, 0)
	if b.Confidence == nil || *b.Confidence != 0.9 {
		t.Errorf("Confidence: got %v", b.Confidence)
	}
}

func TestParseDetectAnomalyClause(t *testing.T) {
	prog := mustParse(t, `
detect "Unusual consumption" {
  for records where type == "stock_item"
  is anomaly compared_to last 12 weeks
  flag matching items
  priority HIGH
}`)
	b := block[*ast.DetectBlock](t, prog, 0)
	if b.Anomaly == nil {
		t.Fatal("Anomaly: nil")
	}
	if b.Anomaly.Window.Value != 12 || b.Anomaly.Window.Unit != "weeks" {
		t.Errorf("Anomaly.Window: got %+v", b.Anomaly.Window)
	}
}

// ─── rule ──────────────────────────────────────────────────────────────────────

func TestParseRuleWithBlock(t *testing.T) {
	prog := mustParse(t, `
rule "No assignment during maintenance" {
  for records where type == "item"
  block "assign"
  reason "Item has open maintenance ticket"
}`)
	b := block[*ast.RuleBlock](t, prog, 0)
	if b.Name != "No assignment during maintenance" {
		t.Errorf("Name: got %q", b.Name)
	}
	if b.Block == nil || *b.Block != "assign" {
		t.Errorf("Block: got %v", b.Block)
	}
	if b.Reason == nil || b.Reason.Raw != "Item has open maintenance ticket" {
		t.Errorf("Reason: got %v", b.Reason)
	}
}

func TestParseRuleWithWhen(t *testing.T) {
	prog := mustParse(t, `
rule "Regional data restriction" {
  when tool_action starts_with "inventory"
  block reason "You don't have access to this organisational unit"
}`)
	b := block[*ast.RuleBlock](t, prog, 0)
	if b.When == nil {
		t.Fatal("When: nil")
	}
	if b.Reason == nil {
		t.Fatal("Reason: nil")
	}
}

func TestParseRuleWithEvery(t *testing.T) {
	prog := mustParse(t, `
rule "Brake inspection every 20k km" {
  for records where category == "van"
  every 20000 km on attr "km"
  requires "brake_inspection"
  priority HIGH
}`)
	b := block[*ast.RuleBlock](t, prog, 0)
	if b.Every == nil {
		t.Fatal("Every: nil")
	}
	if b.Every.Value != 20000 || b.Every.Unit != "km" || b.Every.OnAttr != "km" {
		t.Errorf("Every: got %+v", b.Every)
	}
	if b.Requires == nil || b.Requires.What != "brake_inspection" {
		t.Errorf("Requires: got %v", b.Requires)
	}
}

func TestParseRuleWithApproval(t *testing.T) {
	prog := mustParse(t, `
rule "Manager approval for high value" {
  for records where is "high_value"
  before "status_change"
  requires approval from role "manager"
  reason "Items over 10,000 require manager approval"
}`)
	b := block[*ast.RuleBlock](t, prog, 0)
	if b.Before == nil || *b.Before != "status_change" {
		t.Errorf("Before: got %v", b.Before)
	}
	if b.Requires == nil || b.Requires.Approval == nil || b.Requires.Approval.Role != "manager" {
		t.Errorf("Requires.Approval: got %v", b.Requires)
	}
}

// ─── recommend ─────────────────────────────────────────────────────────────────

func TestParseRecommend(t *testing.T) {
	prog := mustParse(t, `
recommend "Order cement" {
  when detect "Cement running low" matches
  suggest "Order more cement"
  priority HIGH
}`)
	b := block[*ast.RecommendBlock](t, prog, 0)
	if b.Name != "Order cement" {
		t.Errorf("Name: got %q", b.Name)
	}
	bmc, ok := b.When.(*ast.BlockMatchesCondition)
	if !ok {
		t.Fatalf("When: expected BlockMatchesCondition, got %T", b.When)
	}
	if bmc.Kind != "detect" || bmc.Name != "Cement running low" {
		t.Errorf("BlockMatchesCondition: got %+v", bmc)
	}
	if b.Suggest == nil || b.Suggest.Raw != "Order more cement" {
		t.Errorf("Suggest: got %v", b.Suggest)
	}
}

func TestParseRecommendWithCalculate(t *testing.T) {
	prog := mustParse(t, `
recommend "Order cement" {
  when detect "Cement running low" matches
  calculate avg_weekly from activities within last 30 days
  suggest "Order {avg_weekly * 4} bags"
}`)
	b := block[*ast.RecommendBlock](t, prog, 0)
	if len(b.Calculate) != 1 {
		t.Fatalf("Calculate: expected 1, got %d", len(b.Calculate))
	}
	calc := b.Calculate[0]
	if calc.Name != "avg_weekly" || calc.From != "activities" {
		t.Errorf("Calculate: got %+v", calc)
	}
	if calc.Within == nil || calc.Within.Value != 30 || calc.Within.Unit != "days" {
		t.Errorf("Calculate.Within: got %v", calc.Within)
	}
}

// ─── define ────────────────────────────────────────────────────────────────────

func TestParseDefine(t *testing.T) {
	prog := mustParse(t, `
define "high_value" {
  attr "price" > 10000
}`)
	b := block[*ast.DefineBlock](t, prog, 0)
	if b.Name != "high_value" {
		t.Errorf("Name: got %q", b.Name)
	}
	if len(b.Conditions) != 1 {
		t.Errorf("Conditions: expected 1, got %d", len(b.Conditions))
	}
}

func TestParseDefineWithParams(t *testing.T) {
	prog := mustParse(t, `
define "overdue" {
  attr "last_service_date" older_than 90 days
}`)
	b := block[*ast.DefineBlock](t, prog, 0)
	if len(b.Conditions) != 1 {
		t.Errorf("Conditions: expected 1, got %d", len(b.Conditions))
	}
	_, ok := b.Conditions[0].(*ast.TemporalCondition)
	if !ok {
		t.Errorf("Condition: expected TemporalCondition, got %T", b.Conditions[0])
	}
}

// ─── workflow ──────────────────────────────────────────────────────────────────

func TestParseWorkflow(t *testing.T) {
	prog := mustParse(t, `
workflow "Onboard new team member" {
  step "create_person" {
    mcp "hr" "create-person" {
      first_name context.first_name
    }
  }
  step "assign_equipment" depends_on "create_person" {
    mcp "inventory" "assign-item" {
      person_id step("create_person").result.id
    }
  }
}`)
	b := block[*ast.WorkflowBlock](t, prog, 0)
	if b.Name != "Onboard new team member" {
		t.Errorf("Name: got %q", b.Name)
	}
	if len(b.Steps) != 2 {
		t.Fatalf("Steps: expected 2, got %d", len(b.Steps))
	}
	if b.Steps[0].Name != "create_person" {
		t.Errorf("Step[0].Name: got %q", b.Steps[0].Name)
	}
	if b.Steps[1].Name != "assign_equipment" {
		t.Errorf("Step[1].Name: got %q", b.Steps[1].Name)
	}
	if len(b.Steps[1].DependsOn) != 1 || b.Steps[1].DependsOn[0] != "create_person" {
		t.Errorf("Step[1].DependsOn: got %v", b.Steps[1].DependsOn)
	}
}

func TestParseWorkflowMapExpr(t *testing.T) {
	prog := mustParse(t, `
workflow "Delete items" {
  step "find" {
    mcp "srv" "list" {
      query "test"
      collect_all true
    }
  }
  step "delete" depends_on "find" {
    mcp "srv" "batch-delete" {
      ids step("find").result.items.map(id)
    }
  }
}`)
	b := block[*ast.WorkflowBlock](t, prog, 0)
	if len(b.Steps) != 2 {
		t.Fatalf("Steps: expected 2, got %d", len(b.Steps))
	}
	// Verify collect_all arg on step 0
	ca, ok := b.Steps[0].MCPCall.Args["collect_all"]
	if !ok {
		t.Fatal("missing collect_all arg")
	}
	if lit, ok := ca.(*ast.LiteralExpr); !ok || lit.Value != true {
		t.Errorf("collect_all: got %v", ca)
	}
	// Verify .map(id) produces a MapExpr
	ids := b.Steps[1].MCPCall.Args["ids"]
	me, ok := ids.(*ast.MapExpr)
	if !ok {
		t.Fatalf("ids arg: expected MapExpr, got %T", ids)
	}
	if me.Field != "id" {
		t.Errorf("MapExpr.Field: got %q", me.Field)
	}
	src, ok := me.Source.(*ast.StepResultExpr)
	if !ok {
		t.Fatalf("MapExpr.Source: expected StepResultExpr, got %T", me.Source)
	}
	if src.StepName != "find" || src.Field != "result.items" {
		t.Errorf("StepResultExpr: got step=%q field=%q", src.StepName, src.Field)
	}
}

// ─── top-level ML blocks ───────────────────────────────────────────────────────

func TestParseForecastBlock(t *testing.T) {
	prog := mustParse(t, `
forecast "Cement stock-out date" {
  for records where type == "stock_item"
  series attr "current_stock" over last 30 days
  label "{item.name}: hits zero in ~{days_until} days"
  priority CRITICAL
}`)
	b := block[*ast.ForecastBlock](t, prog, 0)
	if b.Name != "Cement stock-out date" {
		t.Errorf("Name: got %q", b.Name)
	}
	if b.Series.Window.Value != 30 || b.Series.Window.Unit != "days" {
		t.Errorf("Series.Window: got %+v", b.Series.Window)
	}
}

func TestParsePredictBlock(t *testing.T) {
	prog := mustParse(t, `
predict "Equipment failure risk" {
  for records where type == "item"
  features [
    attr "operating_hours",
    attr "repair_count"
  ]
  confidence >= 0.7
  label "{item.name}: failure risk"
  priority HIGH
}`)
	b := block[*ast.PredictBlock](t, prog, 0)
	if b.Name != "Equipment failure risk" {
		t.Errorf("Name: got %q", b.Name)
	}
	if len(b.Features) != 2 {
		t.Errorf("Features: expected 2, got %d", len(b.Features))
	}
	if b.Confidence == nil || *b.Confidence != 0.7 {
		t.Errorf("Confidence: got %v", b.Confidence)
	}
}

// ─── conditions ────────────────────────────────────────────────────────────────

func TestParseMembershipCondition(t *testing.T) {
	prog := mustParse(t, `
detect "In list" {
  for records where status in ["active", "pending"]
  flag matching items
}`)
	b := block[*ast.DetectBlock](t, prog, 0)
	cond := b.Selector.Conditions[0].(*ast.MembershipCondition)
	if cond.Negated {
		t.Error("Negated: expected false")
	}
	if len(cond.Members) != 2 {
		t.Errorf("Members: expected 2, got %d", len(cond.Members))
	}
}

func TestParseNotInMembership(t *testing.T) {
	prog := mustParse(t, `
detect "Not in list" {
  for records where status not in ["closed", "archived"]
  flag matching items
}`)
	b := block[*ast.DetectBlock](t, prog, 0)
	cond := b.Selector.Conditions[0].(*ast.MembershipCondition)
	if !cond.Negated {
		t.Error("Negated: expected true")
	}
}

func TestParseStringMatchConditions(t *testing.T) {
	prog := mustParse(t, `
rule "String match" {
  when tool_action starts_with "inventory"
  block "action"
}`)
	b := block[*ast.RuleBlock](t, prog, 0)
	cond, ok := b.When.(*ast.StringMatchCondition)
	if !ok {
		t.Fatalf("When: expected StringMatchCondition, got %T", b.When)
	}
	if cond.Op != "starts_with" || cond.Value != "inventory" {
		t.Errorf("StringMatch: op=%q value=%q", cond.Op, cond.Value)
	}
}

func TestParseIsCondition(t *testing.T) {
	prog := mustParse(t, `
detect "Is high value" {
  for records where is "high_value"
  flag matching items
}`)
	b := block[*ast.DetectBlock](t, prog, 0)
	cond, ok := b.Selector.Conditions[0].(*ast.IsCondition)
	if !ok {
		t.Fatalf("Condition: expected IsCondition, got %T", b.Selector.Conditions[0])
	}
	if cond.Name != "high_value" {
		t.Errorf("IsCondition.Name: got %q", cond.Name)
	}
}

func TestParseTemporalCondition(t *testing.T) {
	prog := mustParse(t, `
detect "Overdue" {
  for records where attr "last_service_date" older_than 90 days
  flag matching items
}`)
	b := block[*ast.DetectBlock](t, prog, 0)
	cond, ok := b.Selector.Conditions[0].(*ast.TemporalCondition)
	if !ok {
		t.Fatalf("Condition: expected TemporalCondition, got %T", b.Selector.Conditions[0])
	}
	if cond.Op != "older_than" || cond.Value.Value != 90 || cond.Value.Unit != "days" {
		t.Errorf("TemporalCondition: %+v", cond)
	}
}

func TestParseAnomalyCondition(t *testing.T) {
	prog := mustParse(t, `
detect "Unusual" {
  for records where attr "weekly_consumption" is anomaly compared_to last 12 weeks
  flag matching items
}`)
	b := block[*ast.DetectBlock](t, prog, 0)
	cond, ok := b.Selector.Conditions[0].(*ast.AnomalyCondition)
	if !ok {
		t.Fatalf("Condition: expected AnomalyCondition, got %T", b.Selector.Conditions[0])
	}
	if cond.Window.Value != 12 || cond.Window.Unit != "weeks" {
		t.Errorf("AnomalyCondition.Window: %+v", cond.Window)
	}
}

func TestParseChangedToCondition(t *testing.T) {
	prog := mustParse(t, `
predict "Failure" {
  for records where status changed_to "defective"
  features [attr "age"]
  label "risk"
}`)
	b := block[*ast.PredictBlock](t, prog, 0)
	cond, ok := b.Selector.Conditions[0].(*ast.ChangedToCondition)
	if !ok {
		t.Fatalf("Condition: expected ChangedToCondition, got %T", b.Selector.Conditions[0])
	}
	if cond.Attribute != "status" {
		t.Errorf("Attribute: got %q", cond.Attribute)
	}
}

func TestParseAndOrChaining(t *testing.T) {
	prog := mustParse(t, `
detect "Chained" {
  for records where type == "item"
    and status == "active"
    and attr "km" > 1000
  flag matching items
}`)
	b := block[*ast.DetectBlock](t, prog, 0)
	// Root should be a LogicalCondition (and)
	_, ok := b.Selector.Conditions[0].(*ast.LogicalCondition)
	if !ok {
		t.Fatalf("Expected LogicalCondition root, got %T", b.Selector.Conditions[0])
	}
}

func TestParseNotCondition(t *testing.T) {
	prog := mustParse(t, `
detect "Not active" {
  for records where not status == "inactive"
  flag matching items
}`)
	b := block[*ast.DetectBlock](t, prog, 0)
	_, ok := b.Selector.Conditions[0].(*ast.NotCondition)
	if !ok {
		t.Fatalf("Expected NotCondition, got %T", b.Selector.Conditions[0])
	}
}

// ─── Error recovery ────────────────────────────────────────────────────────────

func TestParseErrorRecovery(t *testing.T) {
	// Bad block, followed by valid block — must recover and parse the second
	tokens, _ := lexer.Lex("test.talon", `
detect "Bad block" {
  @@@ invalid syntax @@@
}
detect "Good block" {
  for records where status == "ok"
  flag matching items
}`)
	prog, diags := Parse("test.talon", tokens)
	if !diags.HasErrors() {
		t.Fatal("expected parse errors for bad syntax")
	}
	// Should still have parsed the good block
	var found bool
	for _, b := range prog.Blocks {
		if db, ok := b.(*ast.DetectBlock); ok && db.Name == "Good block" {
			found = true
		}
	}
	if !found {
		t.Error("error recovery failed: 'Good block' not in AST")
	}
}

// ─── Multiple blocks ───────────────────────────────────────────────────────────

func TestParseMultipleBlocks(t *testing.T) {
	prog := mustParse(t, `
detect "D1" {
  for records where status == "active"
  flag matching items
}
rule "R1" {
  for records where status == "pending"
  block "action"
}
define "helper" {
  attr "price" > 100
}`)
	if len(prog.Blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(prog.Blocks))
	}
	block[*ast.DetectBlock](t, prog, 0)
	block[*ast.RuleBlock](t, prog, 1)
	block[*ast.DefineBlock](t, prog, 2)
}

// ─── test-block ML assertions ─────────────────────────────────────────────────

func TestParseTestAssertionsScoreAndThreshold(t *testing.T) {
	prog := mustParse(t, `
test "anomaly trace" {
  given {
    record 1 type "stock_item" status "active"
    attr 1 "weekly_consumption" 50
  }
  when detect "Unusual consumption"
  expect {
    flagged 1
    score 1 > 2.5
    threshold ~= 95
    threshold >= 90
  }
}`)
	if len(prog.Blocks) != 1 {
		t.Fatalf("blocks: %d", len(prog.Blocks))
	}
	tb, ok := prog.Blocks[0].(*ast.TestBlock)
	if !ok {
		t.Fatalf("block type %T", prog.Blocks[0])
	}
	if len(tb.Expect) != 4 {
		t.Fatalf("want 4 assertions, got %d", len(tb.Expect))
	}
	score := tb.Expect[1]
	if score.Kind != "score" || score.ID != 1 || score.Op != ">" || score.Value != "2.5" {
		t.Errorf("score: %+v", score)
	}
	thr := tb.Expect[2]
	if thr.Kind != "threshold" || thr.Op != "~=" || thr.Value != "95" {
		t.Errorf("threshold ~=: %+v", thr)
	}
	thr2 := tb.Expect[3]
	if thr2.Kind != "threshold" || thr2.Op != ">=" || thr2.Value != "90" {
		t.Errorf("threshold >=: %+v", thr2)
	}
}

// ─── learned_threshold expression ─────────────────────────────────────────────

func TestParseLearnedThresholdInCompare(t *testing.T) {
	prog := mustParse(t, `
detect "High mileage" {
  for records where type == "item"
    and attr "km" > learned_threshold p95 of attr "km" over last 90 days
  flag matching items
  priority HIGH
}`)
	b := block[*ast.DetectBlock](t, prog, 0)
	if len(b.Selector.Conditions) != 1 {
		t.Fatalf("want 1 selector cond, got %d", len(b.Selector.Conditions))
	}
	log, ok := b.Selector.Conditions[0].(*ast.LogicalCondition)
	if !ok {
		t.Fatalf("want LogicalCondition, got %T", b.Selector.Conditions[0])
	}
	cmp, ok := log.Right.(*ast.CompareCondition)
	if !ok {
		t.Fatalf("want CompareCondition on right, got %T", log.Right)
	}
	if cmp.Op != ">" {
		t.Errorf("op: got %q", cmp.Op)
	}
	lt, ok := cmp.Right.(*ast.LearnedThresholdExpr)
	if !ok {
		t.Fatalf("want LearnedThresholdExpr, got %T", cmp.Right)
	}
	if lt.Method != "p95" {
		t.Errorf("method: got %q", lt.Method)
	}
	sub, ok := lt.Subject.(*ast.AttrExpr)
	if !ok || sub.Name != "km" {
		t.Errorf("subject: got %+v", lt.Subject)
	}
	if lt.Window.Value != 90 || lt.Window.Unit != "days" {
		t.Errorf("window: got %+v", lt.Window)
	}
}

// ─── Nested recommend inside detect ───────────────────────────────────────────

func TestParseNestedRecommend(t *testing.T) {
	prog := mustParse(t, `
detect "Low stock" {
  for records where type == "stock_item"
  flag matching items
  priority HIGH
  recommend "Reorder" {
    when detect "Low stock" matches
    suggest "Place reorder"
  }
}`)
	b := block[*ast.DetectBlock](t, prog, 0)
	if b.Recommend == nil {
		t.Fatal("Recommend: nil")
	}
	if b.Recommend.Name != "Reorder" {
		t.Errorf("Recommend.Name: got %q", b.Recommend.Name)
	}
}

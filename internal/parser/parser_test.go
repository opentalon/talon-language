package parser

import (
	"strings"
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

// ─── find related (PPR) ───────────────────────────────────────────────────────

func TestParseFindRelatedBlockSeeds(t *testing.T) {
	prog := mustParse(t, `
find related "Co-consumed parts" {
  for records where type == "stock_item"
  seeds [808, 809]
  top_k 5
  damping 0.85
  label "{item.name}: ppr {score}"
  priority MEDIUM
}`)
	b := block[*ast.RelatedBlock](t, prog, 0)
	if b.Name != "Co-consumed parts" {
		t.Errorf("Name: got %q", b.Name)
	}
	if len(b.Seeds) != 2 {
		t.Errorf("Seeds: got %d, want 2", len(b.Seeds))
	}
	if b.TopK == nil || *b.TopK != 5 {
		t.Errorf("TopK: got %v", b.TopK)
	}
	if b.Damping == nil || *b.Damping != 0.85 {
		t.Errorf("Damping: got %v", b.Damping)
	}
	if b.Priority == nil || *b.Priority != ast.PriorityMedium {
		t.Errorf("Priority: got %v", b.Priority)
	}
}

func TestParseFindRelatedBlockSingleTo(t *testing.T) {
	prog := mustParse(t, `
find related "Single seed" {
  for records where type == "item"
  to attr "id"
  top_k 3
  tolerance 0.0001
  max_iterations 50
}`)
	b := block[*ast.RelatedBlock](t, prog, 0)
	if b.To == nil {
		t.Fatal("To: nil")
	}
	if b.Tol == nil || *b.Tol != 0.0001 {
		t.Errorf("Tol: got %v", b.Tol)
	}
	if b.MaxIter == nil || *b.MaxIter != 50 {
		t.Errorf("MaxIter: got %v", b.MaxIter)
	}
}

func TestParseFindRelatedClauseInDetect(t *testing.T) {
	prog := mustParse(t, `
detect "Investigate related parts" {
  for records where type == "stock_item"
  flag matching items
  find related to attr "id" top_k 5 damping 0.85
}`)
	b := block[*ast.DetectBlock](t, prog, 0)
	if b.Related == nil {
		t.Fatal("Related clause: nil")
	}
	if b.Related.To == nil {
		t.Fatal("Related.To: nil")
	}
	if b.Related.TopK == nil || *b.Related.TopK != 5 {
		t.Errorf("TopK: got %v", b.Related.TopK)
	}
}

func TestParseFindSimilarStillWorks(t *testing.T) {
	// Make sure adding `find related` did not break the existing
	// `find similar` parser dispatch.
	prog := mustParse(t, `
find similar "Sim" {
  for records where type == "item"
  to attr "id"
  within 0.2
}`)
	b := block[*ast.SimilarBlock](t, prog, 0)
	if b.To == nil || b.Within == nil || *b.Within != 0.2 {
		t.Errorf("similar block parse regressed: %+v", b)
	}
}

// ─── defeasible (strict + overrides) ──────────────────────────────────────────

func TestParseStrictRule(t *testing.T) {
	prog := mustParse(t, `
strict rule "Expired cert blocks assignment" {
  for records where type == "person"
  block "assign"
  reason "Safety certification expired"
}`)
	b := block[*ast.RuleBlock](t, prog, 0)
	if !b.Strict {
		t.Error("expected Strict = true")
	}
}

func TestParseRuleOverrides(t *testing.T) {
	prog := mustParse(t, `
rule "Cleanup crew can delete" {
  when tool_action contains "delete"
  overrides "Block all deletions"
  allow "delete"
  priority HIGH
}`)
	b := block[*ast.RuleBlock](t, prog, 0)
	if len(b.Overrides) != 1 || b.Overrides[0] != "Block all deletions" {
		t.Errorf("Overrides: got %v", b.Overrides)
	}
}

func TestParseRuleMultipleOverrides(t *testing.T) {
	prog := mustParse(t, `
rule "Mega override" {
  when tool_action contains "x"
  overrides "A", "B", "C"
  block "x"
}`)
	b := block[*ast.RuleBlock](t, prog, 0)
	if len(b.Overrides) != 3 || b.Overrides[2] != "C" {
		t.Errorf("Overrides: got %v", b.Overrides)
	}
}

// ─── reactive (on change/assert/retract) ──────────────────────────────────────

func TestParseOnChange(t *testing.T) {
	prog := mustParse(t, `
on change attr "current_stock" {
  logger.warn "stock changed: {item.name}"
}`)
	b := block[*ast.OnBlock](t, prog, 0)
	if b.Trigger != "change" || b.Attr != "current_stock" {
		t.Errorf("Trigger/Attr: got %s / %s", b.Trigger, b.Attr)
	}
	if len(b.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(b.Actions))
	}
	la, ok := b.Actions[0].(*ast.LoggerAction)
	if !ok {
		t.Fatalf("expected LoggerAction, got %T", b.Actions[0])
	}
	if la.Level != "warn" {
		t.Errorf("Level: got %s", la.Level)
	}
}

func TestParseOnAssert(t *testing.T) {
	prog := mustParse(t, `
on assert activity {
  detect "Defective item without ticket"
}`)
	b := block[*ast.OnBlock](t, prog, 0)
	if b.Trigger != "assert" || b.FactType != "activity" {
		t.Errorf("Trigger/FactType: got %s / %s", b.Trigger, b.FactType)
	}
	ref, ok := b.Actions[0].(*ast.BlockRefAction)
	if !ok || ref.Kind != "detect" || ref.Name != "Defective item without ticket" {
		t.Errorf("BlockRefAction: got %+v", b.Actions[0])
	}
}

func TestParseOnRetract(t *testing.T) {
	prog := mustParse(t, `
on retract item {
  logger.info "item removed: {item.id}"
}`)
	b := block[*ast.OnBlock](t, prog, 0)
	if b.Trigger != "retract" || b.FactType != "item" {
		t.Errorf("Trigger/FactType: got %s / %s", b.Trigger, b.FactType)
	}
}

func TestParseOnChangeWorkflowAction(t *testing.T) {
	prog := mustParse(t, `
on change attr "current_stock" to 0 {
  logger.warn "stock-out: {item.name}"
  workflow "Refill stock"
}`)
	b := block[*ast.OnBlock](t, prog, 0)
	if len(b.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(b.Actions))
	}
	ref, ok := b.Actions[1].(*ast.BlockRefAction)
	if !ok || ref.Kind != "workflow" || ref.Name != "Refill stock" {
		t.Errorf("BlockRefAction: got %+v", b.Actions[1])
	}
}

// ─── constraint ───────────────────────────────────────────────────────────────

func TestParseConstraint(t *testing.T) {
	prog := mustParse(t, `
constraint "Stock cannot be negative" {
  for records where type == "stock_item"
  require attr "current_stock" >= 0
  on_violation reject "stock must be non-negative"
}`)
	b := block[*ast.ConstraintBlock](t, prog, 0)
	if b.Name != "Stock cannot be negative" {
		t.Errorf("Name: got %q", b.Name)
	}
	if b.Require == nil {
		t.Fatal("Require: nil")
	}
	if b.OnViolation.Mode != "reject" {
		t.Errorf("Mode: got %q", b.OnViolation.Mode)
	}
	if b.OnViolation.Message != "stock must be non-negative" {
		t.Errorf("Message: got %q", b.OnViolation.Message)
	}
}

func TestParseConstraintQuarantine(t *testing.T) {
	prog := mustParse(t, `
constraint "Suspicious dates" {
  for records where type == "activity"
  require attr "date" >= 0
  on_violation quarantine "needs review"
}`)
	b := block[*ast.ConstraintBlock](t, prog, 0)
	if b.OnViolation.Mode != "quarantine" {
		t.Errorf("Mode: got %q", b.OnViolation.Mode)
	}
}

// ─── Provenance annotations (#3) ──────────────────────────────────────────────

func TestParseDetectScoreAndSource(t *testing.T) {
	prog := mustParse(t, `
detect "auto-discovered: External Workshop correlation" {
  for records where category == "van"
  flag matching items
  confidence 0.82
  source "mined from 14 months of data, 47 matching cases"
}`)
	b := block[*ast.DetectBlock](t, prog, 0)
	if b.Score == nil || *b.Score != 0.82 {
		t.Errorf("Score: got %v, want 0.82", b.Score)
	}
	if b.Source == nil || *b.Source != "mined from 14 months of data, 47 matching cases" {
		t.Errorf("Source: got %v", b.Source)
	}
	// The ML filter form is a separate field and should remain nil.
	if b.Confidence != nil {
		t.Errorf("Confidence (ML filter): unexpectedly set to %v", *b.Confidence)
	}
}

func TestParseDetectConfidenceFilterStillWorks(t *testing.T) {
	// Regression: adding the bare-NUMBER annotation form must not break
	// the existing `>=` filter form on ML-style detect blocks.
	prog := mustParse(t, `
detect "High confidence ML" {
  for records where type == "item"
  flag matching items
  confidence >= 0.9
}`)
	b := block[*ast.DetectBlock](t, prog, 0)
	if b.Confidence == nil || *b.Confidence != 0.9 {
		t.Errorf("Confidence filter: got %v", b.Confidence)
	}
	if b.Score != nil {
		t.Errorf("Score annotation: unexpectedly set to %v", *b.Score)
	}
}

func TestParseRuleScoreAndSource(t *testing.T) {
	prog := mustParse(t, `
rule "Auto: block delete on stale records" {
  when tool_action contains "delete"
  block "delete"
  confidence 0.65
  source "discovered from 90 days of audit logs"
}`)
	b := block[*ast.RuleBlock](t, prog, 0)
	if b.Score == nil || *b.Score != 0.65 {
		t.Errorf("Score: got %v", b.Score)
	}
	if b.Source == nil || *b.Source != "discovered from 90 days of audit logs" {
		t.Errorf("Source: got %v", b.Source)
	}
}

func TestParseRuleRejectsConfidenceFilter(t *testing.T) {
	// On a rule, only the bare `confidence N` provenance form is valid.
	// `confidence >= N` is the ML filter and must be rejected with a
	// clean diagnostic so users learn the correct shape.
	tokens, ld := lexer.Lex("t.talon", `
rule "Bad" {
  when tool_action contains "x"
  block "x"
  confidence >= 0.9
}`)
	if ld.HasErrors() {
		t.Fatalf("lex: %v", ld)
	}
	_, pd := Parse("t.talon", tokens)
	if !pd.HasErrors() {
		t.Fatal("expected a parse error for `confidence >= N` inside rule")
	}
	found := false
	for _, d := range pd {
		if strings.Contains(d.Message, "ML filter") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ML-filter diagnostic, got:\n%v", pd)
	}
}

// ─── Logger statements in detect/rule/recommend (#19) ────────────────────────

func TestParseDetectWithLoggerStatements(t *testing.T) {
	prog := mustParse(t, `
detect "Watch items" {
  for records where type == "item"
  flag matching items
  logger.info "matched: {item.name}"
  logger.warn "{count} items overdue"
}`)
	b := block[*ast.DetectBlock](t, prog, 0)
	if len(b.Loggers) != 2 {
		t.Fatalf("want 2 logger statements, got %d", len(b.Loggers))
	}
	if b.Loggers[0].Level != "info" || b.Loggers[1].Level != "warn" {
		t.Errorf("levels: %v %v", b.Loggers[0].Level, b.Loggers[1].Level)
	}
	if b.Loggers[0].Message.Raw != "matched: {item.name}" {
		t.Errorf("message: %q", b.Loggers[0].Message.Raw)
	}
	// Templates pre-parse Nodes so {item.name} interpolation works at render time.
	if len(b.Loggers[0].Message.Nodes) == 0 {
		t.Error("logger template Nodes not populated by parser")
	}
}

func TestParseRuleWithLoggerStatement(t *testing.T) {
	prog := mustParse(t, `
rule "Audit deletes" {
  when tool_action contains "delete"
  block "delete"
  logger.warn "blocked delete attempt from {context.role}"
}`)
	b := block[*ast.RuleBlock](t, prog, 0)
	if len(b.Loggers) != 1 || b.Loggers[0].Level != "warn" {
		t.Fatalf("want one warn logger, got %v", b.Loggers)
	}
}

func TestParseRecommendWithLoggerStatement(t *testing.T) {
	prog := mustParse(t, `
recommend "Order more" {
  when detect "Low stock" matches
  suggest "order {item.name}"
  logger.info "recommendation fired for {item.name}"
}`)
	b := block[*ast.RecommendBlock](t, prog, 0)
	if len(b.Loggers) != 1 {
		t.Fatalf("want one logger, got %v", b.Loggers)
	}
}

func TestParseLoggerRejectsUnknownLevel(t *testing.T) {
	tokens, _ := lexer.Lex("t.talon", `
detect "Bad" {
  for records where type == "x"
  flag matching items
  logger.shout "loud"
}`)
	_, pd := Parse("t.talon", tokens)
	if !pd.HasErrors() {
		t.Fatal("expected error for unknown log level")
	}
	found := false
	for _, d := range pd {
		if strings.Contains(d.Message, "logger level") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected logger-level diagnostic, got %v", pd)
	}
}

func TestParseDetectRemediate(t *testing.T) {
	prog := mustParse(t, `
detect "Defective without ticket" {
  for records where status == "defective"
  flag matching items
  remediate {
    mcp "inventory" "create-ticket" {
      title "Auto: {item.name}"
      item_id attr "id"
    }
    mcp "slack" "notify" { text "defective item" }
  }
}`)
	b := block[*ast.DetectBlock](t, prog, 0)
	if b.Remediate == nil {
		t.Fatal("Remediate clause not parsed")
	}
	if len(b.Remediate.Calls) != 2 {
		t.Fatalf("expected 2 mcp calls, got %d", len(b.Remediate.Calls))
	}
	first := b.Remediate.Calls[0]
	if first.Server != "inventory" || first.Tool != "create-ticket" {
		t.Errorf("first call target: got %s/%s", first.Server, first.Tool)
	}
	if _, ok := first.Args["title"]; !ok {
		t.Errorf("first call missing title arg: %+v", first.Args)
	}
}

func TestParseRecommendRemediate(t *testing.T) {
	prog := mustParse(t, `
recommend "Order more" {
  when detect "Low stock" matches
  suggest "Order now"
  remediate {
    mcp "inventory" "create-order" { quantity 50 }
  }
}`)
	b := block[*ast.RecommendBlock](t, prog, 0)
	if b.Remediate == nil || len(b.Remediate.Calls) != 1 {
		t.Fatalf("recommend remediate not parsed: %+v", b.Remediate)
	}
}

// Regression: an MCP arg whose name collides with a Talon keyword (e.g.
// `priority`) must parse, not spin the arg loop forever.
func TestParseMCPCallKeywordArgKey(t *testing.T) {
	prog := mustParse(t, `
detect "Act" {
  for records where status == "defective"
  flag matching items
  remediate {
    mcp "inventory" "create-ticket" {
      priority "high"
      status "open"
      title "x"
    }
  }
}`)
	b := block[*ast.DetectBlock](t, prog, 0)
	if b.Remediate == nil || len(b.Remediate.Calls) != 1 {
		t.Fatalf("remediate not parsed: %+v", b.Remediate)
	}
	args := b.Remediate.Calls[0].Args
	for _, k := range []string{"priority", "status", "title"} {
		if _, ok := args[k]; !ok {
			t.Errorf("missing arg %q; got keys %v", k, args)
		}
	}
}

func TestParseEnrich(t *testing.T) {
	prog := mustParse(t, `
enrich "Refresh stock" {
  for records where type == "stock_item"
  stale_after 1 hour
  mcp "inventory" "show-item" { id attr "id" }
  update attr "current_stock" from result.current_stock
  update attr "stock_assigned" from result.data.assigned
}`)
	b := block[*ast.EnrichBlock](t, prog, 0)
	if b.Call == nil || b.Call.Server != "inventory" || b.Call.Tool != "show-item" {
		t.Fatalf("mcp call: %+v", b.Call)
	}
	if b.StaleAfter.Value != 1 || b.StaleAfter.Unit != "hour" {
		t.Errorf("stale_after: %+v", b.StaleAfter)
	}
	if len(b.Updates) != 2 {
		t.Fatalf("expected 2 updates, got %+v", b.Updates)
	}
	if b.Updates[0].Attr != "current_stock" || b.Updates[0].ResultPath != "current_stock" {
		t.Errorf("update 0: %+v", b.Updates[0])
	}
	if b.Updates[1].ResultPath != "data.assigned" {
		t.Errorf("update 1 path: %q", b.Updates[1].ResultPath)
	}
}

func TestParseMCPOnError(t *testing.T) {
	prog := mustParse(t, `
workflow "W" {
  step "s" {
    mcp "inv" "create" {
      title "x"
      on_error {
        retry 3 times
        then log "failed: {error}"
        then skip
      }
    }
  }
}`)
	wf := block[*ast.WorkflowBlock](t, prog, 0)
	oe := wf.Steps[0].MCPCall.OnError
	if oe == nil || len(oe.Actions) != 3 {
		t.Fatalf("on_error actions: %+v", oe)
	}
	if r, ok := oe.Actions[0].(*ast.RetryAction); !ok || r.Times != 3 {
		t.Errorf("action 0 not retry 3: %+v", oe.Actions[0])
	}
	if _, ok := oe.Actions[1].(*ast.LogErrorAction); !ok {
		t.Errorf("action 1 not log: %+v", oe.Actions[1])
	}
	if _, ok := oe.Actions[2].(*ast.SkipAction); !ok {
		t.Errorf("action 2 not skip: %+v", oe.Actions[2])
	}
}

func TestParseTestMockAndMCPCalled(t *testing.T) {
	prog := mustParse(t, `
test "t" {
  given { record 1 type "item" status "defective" }
  mock mcp "inventory" "create-ticket" { returns { id 801  status "open" } }
  when detect "D"
  expect {
    flagged 1
    mcp_called "inventory" "create-ticket" with {
      item_id == 501
      title contains "Drill"
    }
  }
}`)
	tb := block[*ast.TestBlock](t, prog, 0)
	if len(tb.Mocks) != 1 {
		t.Fatalf("mocks: %+v", tb.Mocks)
	}
	if tb.Mocks[0].Server != "inventory" || tb.Mocks[0].Returns["id"] != 801 || tb.Mocks[0].Returns["status"] != "open" {
		t.Errorf("mock: %+v", tb.Mocks[0])
	}
	if len(tb.MCPCalls) != 1 || len(tb.MCPCalls[0].Args) != 2 {
		t.Fatalf("mcp_called: %+v", tb.MCPCalls)
	}
	if tb.MCPCalls[0].Args[0].Name != "item_id" || tb.MCPCalls[0].Args[0].Op != "==" || tb.MCPCalls[0].Args[0].Value != 501 {
		t.Errorf("arg 0: %+v", tb.MCPCalls[0].Args[0])
	}
	if tb.MCPCalls[0].Args[1].Op != "contains" {
		t.Errorf("arg 1 op: %+v", tb.MCPCalls[0].Args[1])
	}
	// flagged assertion still lands in Expect.
	if len(tb.Expect) != 1 || tb.Expect[0].Kind != "flagged" {
		t.Errorf("expect: %+v", tb.Expect)
	}
}

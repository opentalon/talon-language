package testrunner

import (
	"strings"
	"testing"

	"github.com/opentalon/tln-language/internal/lexer"
	"github.com/opentalon/tln-language/internal/parser"
	"github.com/opentalon/tln-language/internal/planner"
	"github.com/opentalon/tln-language/internal/explain"
)

func compileDecisions(t *testing.T, rulesSrc, testSrc string) (map[string][]explain.Decision, []string) {
	t.Helper()
	rt, ld := lexer.Lex("rules.tln", rulesSrc)
	if ld.HasErrors() {
		t.Fatalf("lex rules: %v", ld)
	}
	prog, pd := parser.Parse("rules.tln", rt)
	if pd.HasErrors() {
		t.Fatalf("parse rules: %v", pd)
	}
	plans, planDiags := planner.Plan(prog)
	if planDiags.HasErrors() {
		t.Fatalf("plan: %v", planDiags)
	}

	tt, tld := lexer.Lex("t.tln.test", testSrc)
	if tld.HasErrors() {
		t.Fatalf("lex test: %v", tld)
	}
	tProg, tpd := parser.Parse("t.tln.test", tt)
	if tpd.HasErrors() {
		t.Fatalf("parse test: %v", tpd)
	}

	// Merge: the explain package needs both the rule blocks and the test blocks.
	merged := *prog
	merged.Blocks = append(merged.Blocks, tProg.Blocks...)

	decisions := Decisions(&merged, plans)
	names := make([]string, 0, len(decisions))
	for n := range decisions {
		names = append(names, n)
	}
	return decisions, names
}

func TestDecisionsCementDetectFlagsLowStock(t *testing.T) {
	rules := `
detect "Cement running low" {
  for records where type == "stock_item"
    and attr "current_stock" <= attr "minimum_amount"
  flag matching items
  label "{item.name}: {attr.current_stock} bags left (minimum: {attr.minimum_amount})"
  priority CRITICAL
}`

	tests := `
test "Cement is low" {
  given {
    record 808 type "stock_item" status "active"
    attr 808 "name" "Portland Cement 50kg"
    attr 808 "current_stock" 12
    attr 808 "minimum_amount" 50

    record 809 type "stock_item" status "active"
    attr 809 "name" "Rebar 12mm"
    attr 809 "current_stock" 200
    attr 809 "minimum_amount" 50
  }
  when detect "Cement running low"
  expect {
    flagged 808
    not flagged 809
  }
}`
	decisions, _ := compileDecisions(t, rules, tests)
	got, ok := decisions["Cement is low"]
	if !ok {
		t.Fatalf("no decisions for test")
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 decision, got %d: %+v", len(got), got)
	}
	d := got[0]
	if d.EntityID != 808 {
		t.Errorf("EntityID: got %d, want 808", d.EntityID)
	}
	if d.EntityName != "Portland Cement 50kg" {
		t.Errorf("EntityName: got %q", d.EntityName)
	}
	if d.BlockKind != "detect" {
		t.Errorf("BlockKind: got %q, want detect", d.BlockKind)
	}
	if d.Priority != "CRITICAL" {
		t.Errorf("Priority: got %q, want CRITICAL", d.Priority)
	}
	// Rendered label substitutes the entity's values.
	wantAction := "Portland Cement 50kg: 12 bags left (minimum: 50)"
	if d.Action != wantAction {
		t.Errorf("Action: got %q, want %q", d.Action, wantAction)
	}
	// Why line cites the actual observed comparison.
	if len(d.Why) == 0 || !strings.Contains(d.Why[0], "12") || !strings.Contains(d.Why[0], "50") {
		t.Errorf("Why missing observed values, got %v", d.Why)
	}
	// Evidence excludes type/status/name (header info), keeps current_stock / minimum_amount.
	gotAttrs := map[string]bool{}
	for _, f := range d.Evidence {
		gotAttrs[f.Attribute] = true
	}
	for _, want := range []string{"current_stock", "minimum_amount"} {
		if !gotAttrs[want] {
			t.Errorf("Evidence missing %q. Got %+v", want, d.Evidence)
		}
	}
	if gotAttrs["name"] || gotAttrs["type"] || gotAttrs["status"] {
		t.Errorf("Evidence should exclude boilerplate (name/type/status), got %+v", d.Evidence)
	}
}

func TestDecisionsRecommendChainsToUpstreamDetect(t *testing.T) {
	rules := `
detect "Cement running low" {
  for records where type == "stock_item"
    and attr "current_stock" <= attr "minimum_amount"
  flag matching items
  label "{item.name}: {attr.current_stock} bags left (minimum: {attr.minimum_amount})"
  priority CRITICAL
}

recommend "Order cement" {
  when detect "Cement running low" matches
  suggest "Order {item.name}: 4 weeks of cover at current rate"
  priority HIGH
}`

	tests := `
test "Order recommendation chains to detect" {
  given {
    record 808 type "stock_item" status "active"
    attr 808 "name" "Portland Cement 50kg"
    attr 808 "current_stock" 12
    attr 808 "minimum_amount" 50
  }
  when recommend "Order cement"
  expect {
    flagged 808
  }
}`
	decisions, _ := compileDecisions(t, rules, tests)
	got, ok := decisions["Order recommendation chains to detect"]
	if !ok || len(got) == 0 {
		t.Fatalf("expected recommend decision for entity 808; got %v", decisions)
	}
	d := got[0]
	if d.BlockKind != "recommend" {
		t.Errorf("BlockKind: got %q, want recommend", d.BlockKind)
	}
	if len(d.TriggeredBy) == 0 {
		t.Fatalf("recommend Decision should chain to detect via TriggeredBy, got none")
	}
	up := d.TriggeredBy[0]
	if up.BlockKind != "detect" || up.BlockName != "Cement running low" {
		t.Errorf("upstream: got %s %q, want detect %q", up.BlockKind, up.BlockName, "Cement running low")
	}
	// The recommend's Action should render the suggest template.
	if !strings.Contains(d.Action, "Portland Cement 50kg") {
		t.Errorf("recommend Action did not render entity name: %q", d.Action)
	}
}

func TestRenderTier1CementOutput(t *testing.T) {
	rules := `
detect "Cement running low" {
  for records where type == "stock_item"
    and attr "current_stock" <= attr "minimum_amount"
  flag matching items
  label "{item.name}: {attr.current_stock} bags left (minimum: {attr.minimum_amount})"
  priority CRITICAL
}`
	tests := `
test "Cement is low" {
  given {
    record 808 type "stock_item" status "active"
    attr 808 "name" "Portland Cement 50kg"
    attr 808 "current_stock" 12
    attr 808 "minimum_amount" 50
  }
  when detect "Cement running low"
  expect { flagged 808 }
}`
	decisions, _ := compileDecisions(t, rules, tests)
	d := decisions["Cement is low"][0]
	out := explain.Render(d)

	for _, want := range []string{
		"ACTION    Portland Cement 50kg: 12 bags left",
		"ITEM      Portland Cement 50kg  (entity #808)",
		"PRIORITY  CRITICAL",
		"WHY",
		"EVIDENCE",
		"current_stock = 12",
		"minimum_amount = 50",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q. Got:\n%s", want, out)
		}
	}
}

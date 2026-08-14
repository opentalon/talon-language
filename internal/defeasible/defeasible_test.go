package defeasible

import (
	"strings"
	"testing"

	"github.com/opentalon/tln-language/internal/ast"
)

func rule(name string, opts ...func(*ast.RuleBlock)) *ast.RuleBlock {
	r := &ast.RuleBlock{Name: name}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func strict(r *ast.RuleBlock)                       { r.Strict = true }
func overrides(names ...string) func(*ast.RuleBlock) {
	return func(r *ast.RuleBlock) { r.Overrides = append(r.Overrides, names...) }
}
func priority(p ast.Priority) func(*ast.RuleBlock) {
	return func(r *ast.RuleBlock) { r.Priority = &p }
}

func names(rules []*ast.RuleBlock) []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = r.Name
	}
	return out
}

func TestResolveEmpty(t *testing.T) {
	winners, warnings := Resolve(nil)
	if len(winners) != 0 {
		t.Errorf("expected no winners, got %v", names(winners))
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

func TestResolveSingleRule(t *testing.T) {
	r := rule("only-one")
	winners, warnings := Resolve([]*ast.RuleBlock{r})
	if len(winners) != 1 || winners[0].Name != "only-one" {
		t.Errorf("expected [only-one], got %v", names(winners))
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

func TestResolveOverrideDefeatsTarget(t *testing.T) {
	a := rule("Block all deletions", priority(ast.PriorityLow))
	b := rule("Cleanup crew can delete",
		priority(ast.PriorityHigh),
		overrides("Block all deletions"))
	winners, warnings := Resolve([]*ast.RuleBlock{a, b})
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
	if len(winners) != 1 || winners[0].Name != "Cleanup crew can delete" {
		t.Errorf("expected only the override to win, got %v", names(winners))
	}
}

func TestResolveOverrideChain(t *testing.T) {
	a := rule("A", priority(ast.PriorityLow))
	b := rule("B", priority(ast.PriorityMedium), overrides("A"))
	c := rule("C", priority(ast.PriorityCritical), overrides("B"))
	winners, _ := Resolve([]*ast.RuleBlock{a, b, c})
	if len(winners) != 1 || winners[0].Name != "C" {
		t.Errorf("expected only C to win, got %v", names(winners))
	}
}

func TestResolvePriorityTieWarns(t *testing.T) {
	a := rule("rule-a", priority(ast.PriorityHigh))
	b := rule("rule-b", priority(ast.PriorityHigh))
	winners, warnings := Resolve([]*ast.RuleBlock{a, b})
	if len(winners) != 2 {
		t.Errorf("expected both rules to fire when tied, got %v", names(winners))
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "tie at priority HIGH") {
		t.Errorf("expected a tie warning, got %v", warnings)
	}
}

func TestResolveHigherPriorityWinsWithoutOverride(t *testing.T) {
	low := rule("low", priority(ast.PriorityLow))
	high := rule("high", priority(ast.PriorityHigh))
	winners, warnings := Resolve([]*ast.RuleBlock{low, high})
	if len(winners) != 1 || winners[0].Name != "high" {
		t.Errorf("expected only the high-priority rule, got %v", names(winners))
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

func TestResolveStrictAlwaysFires(t *testing.T) {
	s := rule("Expired cert blocks assignment", strict)
	r := rule("Cleanup can delete",
		priority(ast.PriorityCritical),
		overrides("Expired cert blocks assignment"))
	winners, _ := Resolve([]*ast.RuleBlock{s, r})
	gotNames := names(winners)
	got := strings.Join(gotNames, ",")
	if !strings.Contains(got, "Expired cert blocks assignment") {
		t.Errorf("strict rule was defeated, got %v", gotNames)
	}
	if !strings.Contains(got, "Cleanup can delete") {
		t.Errorf("expected the defeasible rule to also fire, got %v", gotNames)
	}
}

func TestResolveOverrideOfNonMatchingRuleIsNoOp(t *testing.T) {
	r := rule("active", overrides("not-matched"))
	winners, warnings := Resolve([]*ast.RuleBlock{r})
	if len(winners) != 1 || winners[0].Name != "active" {
		t.Errorf("expected active to win, got %v", names(winners))
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}

func TestResolveDefaultPriorityIsMedium(t *testing.T) {
	noPrio := rule("no-priority")
	high := rule("high", priority(ast.PriorityHigh))
	winners, _ := Resolve([]*ast.RuleBlock{noPrio, high})
	if len(winners) != 1 || winners[0].Name != "high" {
		t.Errorf("expected high to beat default-MEDIUM, got %v", names(winners))
	}
}

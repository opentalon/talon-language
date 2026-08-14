package planner

import (
	"testing"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/lexer"
	"github.com/opentalon/tln-language/internal/parser"
)

// TestPlanDeriveNoPlan: a derive block is inlined into referencing queries and
// produces no standalone plan (like define).
func TestPlanDeriveNoPlan(t *testing.T) {
	tokens, _ := lexer.Lex("t.tln", `derive overdue(v) { for records where type == "vehicle" }`)
	prog, pd := parser.Parse("t.tln", tokens)
	if pd.HasErrors() {
		t.Fatalf("parse: %v", pd)
	}
	plans, diags := Plan(prog)
	if diags.HasErrors() {
		t.Fatalf("plan: %v", diags)
	}
	if len(plans) != 0 {
		t.Fatalf("derive block should produce no plan, got %d", len(plans))
	}
}

// TestPlanInlinesPredicateCall: after planning, no PredicateCallCondition
// survives in the referencing block's Filter — the derive body was inlined,
// and its arithmetic condition (attr > attr + N) rode through to a Filter step.
func TestPlanInlinesPredicateCall(t *testing.T) {
	plan := planBlock(t, `
derive overdue(v) {
  for records where type == "vehicle" and attr "km" > attr "last_service_km" + 20000
}

detect "Recall candidates" {
  for records where overdue(v) and attr "model" in ["Transit", "Sprinter"]
  flag matching items
}`, "Recall candidates")

	sawFilterArithmetic := false
	for _, s := range plan.Steps {
		f, ok := s.(*Filter)
		if !ok {
			continue
		}
		for _, c := range f.Conditions {
			if condHasPredicateCall(c) {
				t.Fatal("predicate call was not inlined into the Filter condition")
			}
			if _, ok := c.(*ast.CompareCondition); ok {
				sawFilterArithmetic = true
			}
		}
	}
	if !sawFilterArithmetic {
		t.Error("expected the inlined arithmetic condition to reach a Filter step")
	}
}

func condHasPredicateCall(c ast.Condition) bool {
	switch cc := c.(type) {
	case *ast.LogicalCondition:
		return condHasPredicateCall(cc.Left) || condHasPredicateCall(cc.Right)
	case *ast.NotCondition:
		return condHasPredicateCall(cc.Inner)
	case *ast.PredicateCallCondition:
		return true
	}
	return false
}

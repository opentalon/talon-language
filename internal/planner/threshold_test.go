package planner

import (
	"testing"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/lexer"
	"github.com/opentalon/tln-language/internal/parser"
)

// TestPlanThresholdBlockNoPlan: a cached threshold block is data, not an
// evaluable block — it produces no QueryPlan, and planning must not error.
func TestPlanThresholdBlockNoPlan(t *testing.T) {
	tokens, _ := lexer.Lex("t.tln", `threshold "gap" { value 18200 }`)
	prog, pd := parser.Parse("t.tln", tokens)
	if pd.HasErrors() {
		t.Fatalf("parse: %v", pd)
	}
	plans, diags := Plan(prog)
	if diags.HasErrors() {
		t.Fatalf("plan: %v", diags)
	}
	if len(plans) != 0 {
		t.Fatalf("threshold block should produce no plan, got %d plans", len(plans))
	}
}

// TestPlanInlinesThresholdRef: after planning, no ThresholdRefExpr survives —
// every reference has been rewritten to its cached literal value.
func TestPlanInlinesThresholdRef(t *testing.T) {
	plan := planBlock(t, `
threshold "gap" { value 18200 }
detect "Service overdue" {
  for records where type == "item"
    and attr "km" > attr "last_service_km" + threshold "gap"
  flag matching items
}`, "Service overdue")

	for _, s := range plan.Steps {
		f, ok := s.(*Filter)
		if !ok {
			continue
		}
		for _, c := range f.Conditions {
			if condHasThresholdRef(c) {
				t.Fatal("threshold ref was not inlined into the Filter condition")
			}
		}
	}
}

func condHasThresholdRef(c ast.Condition) bool {
	switch cc := c.(type) {
	case *ast.LogicalCondition:
		return condHasThresholdRef(cc.Left) || condHasThresholdRef(cc.Right)
	case *ast.NotCondition:
		return condHasThresholdRef(cc.Inner)
	case *ast.CompareCondition:
		return exprHasThresholdRef(cc.Left) || exprHasThresholdRef(cc.Right)
	}
	return false
}

func exprHasThresholdRef(e ast.Expr) bool {
	switch ex := e.(type) {
	case *ast.ThresholdRefExpr:
		return true
	case *ast.BinaryExpr:
		return exprHasThresholdRef(ex.Left) || exprHasThresholdRef(ex.Right)
	case *ast.UnaryExpr:
		return exprHasThresholdRef(ex.Operand)
	}
	return false
}

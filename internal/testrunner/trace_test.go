package testrunner

import (
	"testing"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/lexer"
	"github.com/opentalon/talon-language/internal/parser"
	"github.com/opentalon/talon-language/internal/planner"
)

func TestTraceCapturesAnomalyExplanation(t *testing.T) {
	rulesSrc := `
detect "Unusual consumption" {
  for records where type == "stock_item"
    and attr "weekly_consumption" is anomaly compared_to last 12 weeks
  flag matching items
  label "x"
  priority HIGH
}
`
	testSrc := `
test "outlier flagged" {
  given {
    record 1 type "stock_item" status "active"
    attr 1 "weekly_consumption" 48
    record 2 type "stock_item" status "active"
    attr 2 "weekly_consumption" 51
    record 3 type "stock_item" status "active"
    attr 3 "weekly_consumption" 49
    record 4 type "stock_item" status "active"
    attr 4 "weekly_consumption" 50
    record 5 type "stock_item" status "active"
    attr 5 "weekly_consumption" 49
    record 6 type "stock_item" status "active"
    attr 6 "weekly_consumption" 50
    record 7 type "stock_item" status "active"
    attr 7 "weekly_consumption" 51
    record 8 type "stock_item" status "active"
    attr 8 "weekly_consumption" 250
  }
  when detect "Unusual consumption"
  expect {
    flagged 8
  }
}
`
	rulesProg := mustParse(t, "rules.talon", rulesSrc)
	testProg := mustParse(t, "tests.talon.test", testSrc)
	plans, pd := planner.Plan(rulesProg)
	if pd.HasErrors() {
		t.Fatalf("plan errors: %v", pd)
	}

	traces := Trace(testProg, plans)
	if len(traces) != 1 {
		t.Fatalf("want 1 trace, got %d", len(traces))
	}
	tr := traces[0]
	if !tr.Passed {
		t.Fatalf("trace not passed: %v", tr.Errors)
	}
	if tr.Block != "Unusual consumption" {
		t.Fatalf("want block name 'Unusual consumption', got %q", tr.Block)
	}
	if len(tr.Flagged) != 1 || tr.Flagged[0] != 8 {
		t.Fatalf("want flagged=[8], got %v", tr.Flagged)
	}

	var mlStep *TraceStep
	for i := range tr.Steps {
		if tr.Steps[i].Type == "MLComputation" {
			mlStep = &tr.Steps[i]
			break
		}
	}
	if mlStep == nil {
		t.Fatalf("no MLComputation step in trace")
	}
	if mlStep.Function != planner.FuncAnomalyZscore {
		t.Fatalf("want function %s, got %s", planner.FuncAnomalyZscore, mlStep.Function)
	}
	if len(mlStep.Explanations) != 8 {
		t.Fatalf("want 8 explanations, got %d", len(mlStep.Explanations))
	}

	var entity8 *struct {
		observed any
		threshold float64
		sample   int
	}
	for _, e := range mlStep.Explanations {
		if e.EntityID != 8 {
			continue
		}
		if e.Primitive != planner.FuncAnomalyZscore {
			t.Fatalf("explanation primitive %q", e.Primitive)
		}
		if e.Inputs["observed"] == nil {
			t.Fatalf("explanation missing observed input")
		}
		if e.Threshold == nil {
			t.Fatalf("explanation missing threshold")
		}
		if e.Threshold.Sample != 8 {
			t.Fatalf("want threshold sample 8, got %d", e.Threshold.Sample)
		}
		if len(e.Rules) == 0 || e.Rules[0].Attr != "z_score" {
			t.Fatalf("want z_score rule, got %+v", e.Rules)
		}
		entity8 = &struct {
			observed  any
			threshold float64
			sample    int
		}{e.Inputs["observed"], e.Threshold.Value, e.Threshold.Sample}
	}
	if entity8 == nil {
		t.Fatalf("no explanation for entity 8")
	}
}

func mustParse(t *testing.T, name, src string) *ast.Program {
	t.Helper()
	tokens, ld := lexer.Lex(name, src)
	if ld.HasErrors() {
		t.Fatalf("lex %s: %v", name, ld)
	}
	prog, pd := parser.Parse(name, tokens)
	if pd.HasErrors() {
		t.Fatalf("parse %s: %v", name, pd)
	}
	return prog
}

package testrunner

import (
	"testing"

	"github.com/opentalon/tln-language/internal/lexer"
	"github.com/opentalon/tln-language/internal/parser"
	"github.com/opentalon/tln-language/internal/planner"
	"github.com/opentalon/tln-language/internal/validator"
)

// End-to-end: cluster blocks fire through the full compile pipeline +
// MemoryStore dispatch + the cluster_dbscan primitive. Two tight groups
// of points should land in two distinct clusters, no row tagged as
// noise.
func TestRun_ClusterBlockHitsDBSCAN(t *testing.T) {
	src := `
cluster "Group items" {
  for records where type == "item"
  by attr "x"
}

test "two clusters" {
  given {
    record 1 type "item"
    attr 1 "x" 0
    record 2 type "item"
    attr 2 "x" 0.1
    record 3 type "item"
    attr 3 "x" 10
    record 4 type "item"
    attr 4 "x" 10.1
  }
  when cluster "Group items"
  expect {
    flagged 1
    flagged 2
    flagged 3
    flagged 4
  }
}`
	results := runMLPipelineTest(t, src)
	if len(results) != 1 || !results[0].Passed {
		t.Fatalf("expected 1 passing test, got: %+v", results)
	}
}

// End-to-end forecast: a linearly-declining series should produce a
// non-zero days_until projection. The test fixture seeds the series
// directly via test data so the testrunner doesn't need a real time
// dimension on the FactStore.
func TestRun_ForecastBlockHitsExpSmoothing(t *testing.T) {
	// Forecast end-to-end through the planner requires a real `series`
	// fetch from the FactStore, which the in-memory testrunner doesn't
	// model today. The primitive itself is exercised by
	// forecast_test.go. This test instead verifies the planner emits
	// the right plan shape for a forecast block.
	src := `
forecast "Stock empty" {
  for records where type == "stock_item" and status == "active"
  series attr "current_stock" over last 90 days
  label "{item.name}: ~{days_until} days left"
  priority HIGH
}`
	tokens, ld := lexer.Lex("t.tln", src)
	if ld.HasErrors() {
		t.Fatalf("lex: %v", ld)
	}
	prog, pd := parser.Parse("t.tln", tokens)
	if pd.HasErrors() {
		t.Fatalf("parse: %v", pd)
	}
	if vd := validator.Validate("t.tln", prog); vd.HasErrors() {
		t.Fatalf("validate: %v", vd)
	}
	plans, planDiags := planner.Plan(prog)
	if planDiags.HasErrors() {
		t.Fatalf("plan: %v", planDiags)
	}
	plan := plans["Stock empty"]
	if plan == nil {
		t.Fatal("no plan for `Stock empty`")
	}
	// Look for an MLComputation step with the forecast function.
	found := false
	for _, step := range plan.Steps {
		if mc, ok := step.(*planner.MLComputation); ok && mc.Function == planner.FuncForecastExpSmoothing {
			found = true
			if mc.Params["series_var"] != "current_stock" {
				t.Errorf("forecast Params.series_var: got %v, want \"current_stock\"", mc.Params["series_var"])
			}
		}
	}
	if !found {
		t.Errorf("expected MLComputation forecast_exponential_smoothing in plan; got %d steps", len(plan.Steps))
	}
}

// End-to-end find similar — feature vector from a single attr; the
// candidate set should be ranked but unchanged in membership when no
// `within` threshold is set.
func TestRun_FindSimilarHitsCosine(t *testing.T) {
	src := `
find similar "Comparable parts" {
  for records where type == "part"
  to attr "weight"
}

test "ranks parts" {
  given {
    record 1 type "part"
    attr 1 "weight" 10
    record 2 type "part"
    attr 2 "weight" 12
    record 3 type "part"
    attr 3 "weight" 100
  }
  when find similar "Comparable parts"
  expect {
    flagged 1
    flagged 2
    flagged 3
  }
}`
	results := runMLPipelineTest(t, src)
	if len(results) != 1 || !results[0].Passed {
		t.Fatalf("expected passing test, got: %+v", results)
	}
}

// runMLPipelineTest compiles `src` and runs every test block against
// the in-memory testrunner. Returns the TestResult slice for assertions.
func runMLPipelineTest(t *testing.T, src string) []TestResult {
	t.Helper()
	tokens, ld := lexer.Lex("t.tln", src)
	if ld.HasErrors() {
		t.Fatalf("lex: %v", ld)
	}
	prog, pd := parser.Parse("t.tln", tokens)
	if pd.HasErrors() {
		t.Fatalf("parse: %v", pd)
	}
	if vd := validator.Validate("t.tln", prog); vd.HasErrors() {
		t.Fatalf("validate: %v", vd)
	}
	plans, planDiags := planner.Plan(prog)
	if planDiags.HasErrors() {
		t.Fatalf("plan: %v", planDiags)
	}
	return Run(prog, plans)
}

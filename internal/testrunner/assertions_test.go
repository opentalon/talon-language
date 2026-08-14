package testrunner

import (
	"strings"
	"testing"

	"github.com/opentalon/tln-language/internal/planner"
)

func TestRunScoreAndThresholdAssertionsPass(t *testing.T) {
	rulesSrc := `
detect "High mileage" {
  for records where type == "item"
    and attr "km" > learned_threshold p95 of attr "km" over last 90 days
  flag matching items
  label "x"
  priority HIGH
}
`
	testSrc := `
test "top mileage" {
  given {
    record 1 type "item" status "active"
    attr 1 "km" 10
    record 2 type "item" status "active"
    attr 2 "km" 20
    record 3 type "item" status "active"
    attr 3 "km" 30
    record 4 type "item" status "active"
    attr 4 "km" 40
    record 5 type "item" status "active"
    attr 5 "km" 50
    record 6 type "item" status "active"
    attr 6 "km" 60
    record 7 type "item" status "active"
    attr 7 "km" 70
    record 8 type "item" status "active"
    attr 8 "km" 80
    record 9 type "item" status "active"
    attr 9 "km" 90
    record 10 type "item" status "active"
    attr 10 "km" 100
  }
  when detect "High mileage"
  expect {
    flagged 10
    score 10 == 100
    threshold ~= 95
    threshold >= 90
    threshold <= 100
  }
}
`
	results := runResults(t, rulesSrc, testSrc)
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if !results[0].Passed {
		t.Fatalf("assertions failed: %v", results[0].Errors)
	}
}

func TestRunThresholdAssertionFailsWhenOutOfTolerance(t *testing.T) {
	rulesSrc := `
detect "High mileage" {
  for records where type == "item"
    and attr "km" > learned_threshold p95 of attr "km" over last 90 days
  flag matching items
  label "x"
  priority HIGH
}
`
	testSrc := `
test "off threshold" {
  given {
    record 1 type "item" status "active"
    attr 1 "km" 10
    record 2 type "item" status "active"
    attr 2 "km" 20
    record 3 type "item" status "active"
    attr 3 "km" 30
    record 4 type "item" status "active"
    attr 4 "km" 100
  }
  when detect "High mileage"
  expect {
    threshold ~= 1
  }
}
`
	results := runResults(t, rulesSrc, testSrc)
	if results[0].Passed {
		t.Fatalf("expected failure")
	}
	if len(results[0].Errors) == 0 || !strings.Contains(results[0].Errors[0], "threshold:") {
		t.Fatalf("want threshold error, got %v", results[0].Errors)
	}
}

func TestRunScoreAssertionFailsWhenEntityNotMLScored(t *testing.T) {
	rulesSrc := `
detect "All items" {
  for records where type == "item"
  flag matching items
  priority HIGH
}
`
	testSrc := `
test "no ml" {
  given {
    record 1 type "item" status "active"
    record 2 type "item" status "active"
  }
  when detect "All items"
  expect {
    score 1 > 0
  }
}
`
	results := runResults(t, rulesSrc, testSrc)
	if results[0].Passed {
		t.Fatalf("expected score assertion to fail when no ML step")
	}
	if !strings.Contains(results[0].Errors[0], "no ML score recorded") {
		t.Fatalf("want 'no ML score recorded', got %v", results[0].Errors)
	}
}

func runResults(t *testing.T, rulesSrc, testSrc string) []TestResult {
	t.Helper()
	rulesProg := mustParse(t, "rules.tln", rulesSrc)
	testProg := mustParse(t, "tests.tln.test", testSrc)
	plans, pd := planner.Plan(rulesProg)
	if pd.HasErrors() {
		t.Fatalf("plan: %v", pd)
	}
	// Mirror `tln test`: it merges the rules and test programs before
	// running, which is what gives the runner access to the source blocks.
	merged := *rulesProg
	merged.Blocks = append(merged.Blocks, testProg.Blocks...)
	return Run(&merged, plans)
}

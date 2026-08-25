package tln_test

import (
	"os"
	"strings"
	"testing"

	"github.com/opentalon/tln-language/pkg/tln"
)

const testRunnerRules = `
rule "Auto-approve safe changes" {
  for records where type == "pr" and attr "risk" == "low"
  do approve "pr"
}
`

func TestRunTestsPassing(t *testing.T) {
	src := `
test "low risk PR is approved" {
  given {
    record 1 type "pr"
    attr 1 "risk" "low"
  }
  when rule "Auto-approve safe changes"
  expect {
    flagged 1
    did 1 approve "pr"
  }
}
`
	results, err := tln.RunTests(testRunnerRules, src)
	if err != nil {
		t.Fatalf("RunTests: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if !results[0].Passed {
		t.Fatalf("want test to pass, got errors: %v", results[0].Errors)
	}
}

func TestRunTestsFailingAssertion(t *testing.T) {
	src := `
test "high risk PR is wrongly expected to be approved" {
  given {
    record 1 type "pr"
    attr 1 "risk" "high"
  }
  when rule "Auto-approve safe changes"
  expect {
    flagged 1
  }
}
`
	results, err := tln.RunTests(testRunnerRules, src)
	if err != nil {
		t.Fatalf("RunTests: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Passed {
		t.Fatal("want test to fail: risk is high, record 1 should not be flagged")
	}
}

func TestRunTestsRulesLexError(t *testing.T) {
	_, err := tln.RunTests(`rule "broken" { for records where`, `test "x" { when rule "broken" expect {} }`)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	ce, ok := err.(*tln.CompileError)
	if !ok {
		t.Fatalf("want *CompileError, got %T: %v", err, err)
	}
	if ce.Stage != "parse" && ce.Stage != "lex" {
		t.Fatalf("want lex or parse stage, got %q", ce.Stage)
	}
}

func TestRunTestsTestSourceParseError(t *testing.T) {
	_, err := tln.RunTests(testRunnerRules, `test "x" { when rule`)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if _, ok := err.(*tln.CompileError); !ok {
		t.Fatalf("want *CompileError, got %T: %v", err, err)
	}
}

func TestRunTestsUnknownBlockReference(t *testing.T) {
	src := `
test "references a block that doesn't exist" {
  given {
    record 1 type "pr"
  }
  when rule "No such rule"
  expect {
    flagged 1
  }
}
`
	_, err := tln.RunTests(testRunnerRules, src)
	if err == nil {
		t.Fatal("want error for unknown block reference, got nil")
	}
	ce, ok := err.(*tln.CompileError)
	if !ok {
		t.Fatalf("want *CompileError, got %T: %v", err, err)
	}
	if ce.Stage != "validate" {
		t.Fatalf("want validate stage, got %q", ce.Stage)
	}
}

func TestRunTestsAssertionOnVerbRuleNeverDoes(t *testing.T) {
	src := `
test "asserts a verb the rule has no do clause for" {
  given {
    record 1 type "pr"
    attr 1 "risk" "low"
  }
  when rule "Auto-approve safe changes"
  expect {
    did 1 block "pr"
  }
}
`
	_, err := tln.RunTests(testRunnerRules, src)
	if err == nil {
		t.Fatal("want error: rule never does 'block'")
	}
	if _, ok := err.(*tln.CompileError); !ok {
		t.Fatalf("want *CompileError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "block") {
		t.Fatalf("want error mentioning the unmatched verb, got: %v", err)
	}
}

// TestRunTestsMultipleBlocksOrdered pins "one TestResult per test block, in
// source order" — RunTests must produce as many results as test blocks, in
// the order they appear in testSource.
func TestRunTestsMultipleBlocksOrdered(t *testing.T) {
	src := `
test "first: low risk is approved" {
  given {
    record 1 type "pr"
    attr 1 "risk" "low"
  }
  when rule "Auto-approve safe changes"
  expect {
    flagged 1
  }
}

test "second: high risk is not approved" {
  given {
    record 2 type "pr"
    attr 2 "risk" "high"
  }
  when rule "Auto-approve safe changes"
  expect {
    not flagged 2
  }
}
`
	results, err := tln.RunTests(testRunnerRules, src)
	if err != nil {
		t.Fatalf("RunTests: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if results[0].Name != "first: low risk is approved" {
		t.Fatalf("want first result named %q, got %q", "first: low risk is approved", results[0].Name)
	}
	if results[1].Name != "second: high risk is not approved" {
		t.Fatalf("want second result named %q, got %q", "second: high risk is not approved", results[1].Name)
	}
	for _, r := range results {
		if !r.Passed {
			t.Fatalf("want %q to pass, got errors: %v", r.Name, r.Errors)
		}
	}
}

// TestRunTestsABCTuning locks in the one behavior review flagged as at risk
// from reusing compileProgram's already macro/import-resolved *ast.Program
// instead of runTestPair's separate re-parse: a detect block's `tune against
// test "..."` clause must still find its labeled fixture once that fixture
// is a TestBlock appended from testSource, not present in rulesSource's own
// AST. Reuses the repo's own tuned_consumption example/test pair rather than
// a synthetic fixture, since ABC needs a realistic labeled distribution to
// tune against.
func TestRunTestsABCTuning(t *testing.T) {
	rules, err := os.ReadFile("../../examples/tuned_consumption.tln")
	if err != nil {
		t.Fatalf("read rules fixture: %v", err)
	}
	tests, err := os.ReadFile("../../test/tuned_consumption.tln.test")
	if err != nil {
		t.Fatalf("read test fixture: %v", err)
	}

	results, err := tln.RunTests(string(rules), string(tests))
	if err != nil {
		t.Fatalf("RunTests: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	// The fixture's default z=2.5 threshold misses the labeled outliers;
	// only a successful ABC tune (which needs to find the labeled test
	// block via the merged program) recovers them, so a pass here proves
	// tuning ran against the right fixture.
	if !results[0].Passed {
		t.Fatalf("want tuned test to pass, got errors: %v", results[0].Errors)
	}
}

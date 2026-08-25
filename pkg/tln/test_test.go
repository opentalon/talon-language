package tln_test

import (
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

func TestRunTestsTestSourceLexError(t *testing.T) {
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

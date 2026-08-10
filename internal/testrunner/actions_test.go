package testrunner

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/executor"
	"github.com/opentalon/talon-language/internal/factstore"

	"github.com/opentalon/talon-language/internal/diagnostic"
	"github.com/opentalon/talon-language/internal/planner"
)

// The ruleset every test here runs against: one rule with three actions,
// covering a plain literal argument, an attr-valued argument, and an
// interpolated one.
const actionRules = `
rule "Critical path" {
  for records where type == "pr"
    and attr "pr.changed_files" contains "internal/auth/"
  requires "review.senior"
  do require "review.senior"
  do assign "pr" attr "user.owner"
  do comment "pr" "Owned by {attr.user.owner}, {attr.pr.files_changed} files"
}
`

func TestActionAssertionsPass(t *testing.T) {
	testSrc := `
test "critical path fires all three actions" {
  given {
    record 1 type "pr"
    attr 1 "pr.changed_files" ["internal/auth/session.go"]
    attr 1 "pr.files_changed" 2
    attr 1 "user.owner" "@alice"
  }
  when rule "Critical path"
  expect {
    flagged 1
    did 1 require "review.senior"
    did 1 assign "pr" "@alice"
    did 1 comment "pr" contains "Owned by @alice"
    did 1 comment "pr" contains "2 files"
  }
}
`
	results := runResults(t, actionRules, testSrc)
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if !results[0].Passed {
		t.Fatalf("assertions failed: %v", results[0].Errors)
	}
}

// A row the rule never matched fires nothing, so did_not holds and did fails.
func TestActionAssertionsUnmatchedRow(t *testing.T) {
	testSrc := `
test "off the critical path" {
  given {
    record 1 type "pr"
    attr 1 "pr.changed_files" ["README.md"]
    attr 1 "user.owner" "@alice"
  }
  when rule "Critical path"
  expect {
    not flagged 1
    did_not 1 require "review.senior"
  }
}
`
	results := runResults(t, actionRules, testSrc)
	if !results[0].Passed {
		t.Fatalf("assertions failed: %v", results[0].Errors)
	}
}

func TestActionAssertionFailsOnWrongArgument(t *testing.T) {
	testSrc := `
test "wrong owner" {
  given {
    record 1 type "pr"
    attr 1 "pr.changed_files" ["internal/auth/session.go"]
    attr 1 "user.owner" "@alice"
  }
  when rule "Critical path"
  expect {
    did 1 assign "pr" "@carol"
  }
}
`
	results := runResults(t, actionRules, testSrc)
	if results[0].Passed {
		t.Fatal("expected a failure: the rule assigned @alice, not @carol")
	}
	joined := strings.Join(results[0].Errors, "\n")
	// The failure has to name what actually fired, or a red test tells you
	// nothing about which of several actions went wrong.
	if !strings.Contains(joined, "@alice") {
		t.Errorf("failure should report the actual arguments, got: %s", joined)
	}
}

func TestActionAssertionFailsOnUnfiredVerb(t *testing.T) {
	testSrc := `
test "verb never fired" {
  given {
    record 1 type "pr"
    attr 1 "pr.changed_files" ["internal/auth/session.go"]
    attr 1 "user.owner" "@alice"
  }
  when rule "Critical path"
  expect {
    did 1 approve "pr"
  }
}
`
	results := runResults(t, actionRules, testSrc)
	if results[0].Passed {
		t.Fatal("expected a failure: the rule never fires `approve`")
	}
}

// did_not is the assertion that catches an over-firing rule, so it has to fail
// when the action did happen.
func TestNegatedActionAssertionFailsWhenActionFired(t *testing.T) {
	testSrc := `
test "did_not on an action that fired" {
  given {
    record 1 type "pr"
    attr 1 "pr.changed_files" ["internal/auth/session.go"]
    attr 1 "user.owner" "@alice"
  }
  when rule "Critical path"
  expect {
    did_not 1 require "review.senior"
  }
}
`
	results := runResults(t, actionRules, testSrc)
	if results[0].Passed {
		t.Fatal("expected a failure: `require` did fire for row 1")
	}
	if !strings.Contains(strings.Join(results[0].Errors, "\n"), "NOT") {
		t.Errorf("failure should say the action was not expected, got: %v", results[0].Errors)
	}
}

// Fewer matchers than arguments is a prefix match: asserting the verb and its
// target should not require restating an interpolated comment body.
func TestActionAssertionPrefixMatch(t *testing.T) {
	testSrc := `
test "prefix match on args" {
  given {
    record 1 type "pr"
    attr 1 "pr.changed_files" ["internal/auth/session.go"]
    attr 1 "pr.files_changed" 2
    attr 1 "user.owner" "@alice"
  }
  when rule "Critical path"
  expect {
    did 1 comment "pr"
    did 1 comment
  }
}
`
	results := runResults(t, actionRules, testSrc)
	if !results[0].Passed {
		t.Fatalf("assertions failed: %v", results[0].Errors)
	}
}

// More matchers than the action carries can never match — otherwise a typo'd
// extra argument would pass silently.
func TestActionAssertionRejectsExtraArgument(t *testing.T) {
	testSrc := `
test "too many args" {
  given {
    record 1 type "pr"
    attr 1 "pr.changed_files" ["internal/auth/session.go"]
    attr 1 "user.owner" "@alice"
  }
  when rule "Critical path"
  expect {
    did 1 assign "pr" "@alice" "extra"
  }
}
`
	results := runResults(t, actionRules, testSrc)
	if results[0].Passed {
		t.Fatal("expected a failure: `assign` takes two arguments, not three")
	}
}

// An attr argument the row does not carry resolves to nil rather than "", so
// the action still fires and the host can tell the fact was missing.
func TestActionArgumentFromMissingAttr(t *testing.T) {
	testSrc := `
test "missing owner" {
  given {
    record 1 type "pr"
    attr 1 "pr.changed_files" ["internal/auth/session.go"]
  }
  when rule "Critical path"
  expect {
    flagged 1
    did 1 require "review.senior"
    did_not 1 assign "pr" ""
  }
}
`
	results := runResults(t, actionRules, testSrc)
	if !results[0].Passed {
		t.Fatalf("assertions failed: %v", results[0].Errors)
	}
}

// Every matched row gets its own copy of the actions, with its own arguments.
func TestActionsFirePerMatchedRow(t *testing.T) {
	testSrc := `
test "two matched rows" {
  given {
    record 1 type "pr"
    attr 1 "pr.changed_files" ["internal/auth/a.go"]
    attr 1 "user.owner" "@alice"
    record 2 type "pr"
    attr 2 "pr.changed_files" ["internal/auth/b.go"]
    attr 2 "user.owner" "@bob"
    record 3 type "pr"
    attr 3 "pr.changed_files" ["docs/readme.md"]
    attr 3 "user.owner" "@carol"
  }
  when rule "Critical path"
  expect {
    count == 2
    did 1 assign "pr" "@alice"
    did 2 assign "pr" "@bob"
    did_not 1 assign "pr" "@bob"
    did_not 3 assign "pr" "@carol"
  }
}
`
	results := runResults(t, actionRules, testSrc)
	if !results[0].Passed {
		t.Fatalf("assertions failed: %v", results[0].Errors)
	}
}

// A typo'd verb in a negated assertion would otherwise pass vacuously: the
// misspelled verb never fires, so `did_not` always holds and the assertion that
// exists to catch over-firing green-lights it. Validate rejects it instead.
func TestValidateRejectsUnknownActionVerb(t *testing.T) {
	testSrc := `
test "typo" {
  given {
    record 1 type "pr"
    attr 1 "pr.changed_files" ["internal/auth/a.go"]
  }
  when rule "Critical path"
  expect {
    did_not 1 aprove "pr"
  }
}
`
	diags := validateSrc(t, actionRules, testSrc)
	if !diags.HasErrors() {
		t.Fatal("expected a diagnostic for a verb the rule never does")
	}
	if !strings.Contains(diags[0].Message, "aprove") {
		t.Errorf("diagnostic should name the verb, got: %v", diags[0].Message)
	}
}

// The verbs the rule does actually carry validate clean, in both forms.
func TestValidateAcceptsKnownActionVerbs(t *testing.T) {
	testSrc := `
test "known verbs" {
  given { record 1 type "pr" }
  when rule "Critical path"
  expect {
    did 1 comment "pr"
    did_not 1 assign "pr"
  }
}
`
	if diags := validateSrc(t, actionRules, testSrc); diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
}

func validateSrc(t *testing.T, rulesSrc, testSrc string) diagnostic.List {
	t.Helper()
	rulesProg := mustParse(t, "rules.talon", rulesSrc)
	testProg := mustParse(t, "tests.talon.test", testSrc)
	plans, pd := planner.Plan(rulesProg)
	if pd.HasErrors() {
		t.Fatalf("plan: %v", pd)
	}
	merged := *rulesProg
	merged.Blocks = append(merged.Blocks, testProg.Blocks...)
	return Validate(&merged, plans)
}

// The test runner and the runtime must resolve `do` clauses identically —
// a `did` assertion that passes while the host receives something else is
// the failure mode promoting this code out of the test runner was meant to
// remove. Same ruleset, same facts, both paths, compared verbatim.
func TestActionsRuntimeParity(t *testing.T) {
	rulesSrc := `
rule "Critical path" {
  for records where type == "pr" and attr "risk" == "low"
  do require "review.senior"
  do assign "pr" attr "user.owner"
  do comment "pr" "Owned by {attr.user.owner}, {attr.files} files"
  do escalate attr "missing.attr"
}
`
	given := []ast.TestDatum{
		{Kind: "record", ID: 4, Fields: map[string]any{"type": "pr"}},
		{Kind: "attr", ID: 4, Fields: map[string]any{"risk": "low"}},
		{Kind: "attr", ID: 4, Fields: map[string]any{"user.owner": "@alice"}},
		{Kind: "attr", ID: 4, Fields: map[string]any{"files": 2.0}},
	}

	prog := mustParse(t, "rules.talon", rulesSrc)
	plans, pd := planner.Plan(prog)
	if pd.HasErrors() {
		t.Fatalf("plan: %v", pd)
	}
	rule := prog.Blocks[0]

	// Test-runner path: in-memory entities, flagged set from the runner.
	fromRunner := FireBlockActions(rule, []int{4}, buildEntities(given), time.Unix(0, 0).UTC())

	// Runtime path: the same program against a seeded MemoryStore.
	store := factstore.NewMemoryStore()
	exec := &executor.Executor{Client: store}
	if _, err := exec.Seed(context.Background(), &ast.Program{
		Blocks: []ast.Block{&ast.TestBlock{Given: given}},
	}); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	results, err := exec.RunAll(context.Background(), plans)
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}
	fromRuntime := results["Critical path"].Actions

	if !reflect.DeepEqual(fromRunner, fromRuntime) {
		t.Fatalf("test runner and runtime disagree:\n runner  %#v\n runtime %#v", fromRunner, fromRuntime)
	}
	if len(fromRuntime) != 4 {
		t.Fatalf("want 4 actions, got %#v", fromRuntime)
	}
	// The missing attr survives as nil on both paths.
	if got := fromRuntime[3].Args; !reflect.DeepEqual(got, []any{nil}) {
		t.Fatalf("missing attr arg: got %#v, want []any{nil}", got)
	}
}

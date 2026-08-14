package constraints

import (
	"testing"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/lexer"
	"github.com/opentalon/tln-language/internal/parser"
)

// parseCondition parses the first selector condition of a throwaway detect
// block, so tests can name a condition in tln source instead of building
// AST nodes by hand.
func parseCondition(t *testing.T, expr string) ast.Condition {
	t.Helper()
	src := `detect "T" { for records where ` + expr + ` }`
	tokens, ld := lexer.Lex("t.tln", src)
	if ld.HasErrors() {
		t.Fatalf("lex %q: %v", expr, ld)
	}
	prog, pd := parser.Parse("t.tln", tokens)
	if pd.HasErrors() {
		t.Fatalf("parse %q: %v", expr, pd)
	}
	det, ok := prog.Blocks[0].(*ast.DetectBlock)
	if !ok || len(det.Selector.Conditions) == 0 {
		t.Fatalf("parse %q: no selector condition", expr)
	}
	return det.Selector.Conditions[0]
}

// A string predicate against a list-valued attribute holds when any element
// satisfies it (issue #158). Both []any and []string shapes reach the
// evaluator depending on how the record was decoded.
func TestStringMatchQuantifiesOverList(t *testing.T) {
	cases := []struct {
		expr  string
		value any
		want  bool
	}{
		{`attr "changed_files" contains "go.mod"`, []any{"go.mod", "main.go"}, true},
		{`attr "changed_files" contains "go.mod"`, []string{"go.mod", "main.go"}, true},
		{`attr "changed_files" contains "go.mod"`, []any{"README.md", "main.go"}, false},
		{`attr "changed_files" starts_with "internal/"`, []any{"main.go", "internal/x.go"}, true},
		{`attr "changed_files" starts_with "internal/"`, []any{"main.go", "cmd/x.go"}, false},
		{`attr "changed_files" ends_with ".go"`, []any{"go.sum", "main.go"}, true},
		{`attr "changed_files" ends_with ".go"`, []any{"go.sum", "README.md"}, false},

		// Unhappy paths: empty list matches nothing; non-string elements are
		// skipped rather than matched or fataled on; a mixed list still
		// matches on its one string element.
		{`attr "changed_files" contains "go.mod"`, []any{}, false},
		{`attr "changed_files" contains "go.mod"`, []any{nil, 42.0, true}, false},
		{`attr "changed_files" contains "go.mod"`, []any{42.0, "go.mod"}, true},
		{`attr "changed_files" contains "go.mod"`, []any{[]any{"go.mod"}}, false},

		// Scalars keep their old behaviour.
		{`attr "changed_files" contains "go.mod"`, "go.mod,main.go", true},
		{`attr "changed_files" contains "go.mod"`, "main.go", false},
	}
	for _, c := range cases {
		cond := parseCondition(t, c.expr)
		got, err := EvalCondition(cond, map[string]any{
			"type":          "pr",
			"changed_files": c.value,
		})
		if err != nil {
			t.Errorf("%s over %#v: unexpected error: %v", c.expr, c.value, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s over %#v = %v, want %v", c.expr, c.value, got, c.want)
		}
	}
}

// `matches` / `matches_phrase` in the Go filter path (constraint blocks, and
// selector conditions the planner can't push into the store) used to fall
// through stringMatch's switch and evaluate to false for every input.
func TestStringMatchFullTextOps(t *testing.T) {
	cases := []struct {
		expr  string
		value any
		want  bool
	}{
		{`attr "notes" matches "fuel"`, "Replaced FUEL pump", true}, // case-insensitive
		{`attr "notes" matches "brake"`, "Replaced fuel pump", false},
		{`attr "notes" matches phrase "fuel pump"`, "Replaced fuel pump", true},
		{`attr "notes" matches phrase "pump fuel"`, "Replaced fuel pump", false},
		{`attr "changed_files" matches "main.go"`, []any{"go.mod", "main.go"}, true},
		{`attr "changed_files" matches "nowhere"`, []any{"go.mod", "main.go"}, false},
		{`attr "changed_files" matches phrase "go.mod"`, []string{"go.mod"}, true},
		{`attr "notes" matches ""`, "anything", false},
	}
	for _, c := range cases {
		cond := parseCondition(t, c.expr)
		got, err := EvalCondition(cond, map[string]any{"notes": c.value, "changed_files": c.value})
		if err != nil {
			t.Errorf("%s over %#v: unexpected error: %v", c.expr, c.value, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s over %#v = %v, want %v", c.expr, c.value, got, c.want)
		}
	}
}

// A non-string, non-list subject is still a program error, not a silent false.
func TestStringMatchNonStringSubjectStillErrors(t *testing.T) {
	cond := parseCondition(t, `attr "changed_files" contains "go.mod"`)
	if _, err := EvalCondition(cond, map[string]any{"changed_files": 42.0}); err == nil {
		t.Error("expected error for numeric subject, got nil")
	}
}

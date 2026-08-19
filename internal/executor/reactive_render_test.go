package executor

import (
	"testing"

	"github.com/opentalon/tln-language/internal/ast"
)

// A workflow step's string arg interpolates against the triggering record's
// row when a reactive fire seeds TriggerRowVar, and passes through verbatim
// otherwise — so plain (non-reactive) workflow runs are unchanged.
func TestResolveExprValue_TriggerRowInterpolation(t *testing.T) {
	lit := &ast.LiteralExpr{Value: "New item {item.name} in {category} (serial {attr.custom_attributes.serial_number})"}

	// No trigger row → verbatim.
	if got := resolveExprValue(lit, map[string]any{}); got != lit.Value {
		t.Fatalf("without trigger row: got %q, want verbatim %q", got, lit.Value)
	}

	// With the triggering record's flat row, {item.name}, bare {category} and
	// {attr.<dotted>} all bind.
	vars := map[string]any{
		TriggerRowVar: map[string]any{
			"name":                            "Bosch Drill",
			"category":                        "Nivellier",
			"custom_attributes.serial_number": "SN-42",
		},
	}
	want := "New item Bosch Drill in Nivellier (serial SN-42)"
	if got := resolveExprValue(lit, vars); got != want {
		t.Fatalf("with trigger row: got %q, want %q", got, want)
	}

	// A ref the row lacks stays literal (unresolved refs render verbatim).
	missing := &ast.LiteralExpr{Value: "{nope}"}
	if got := resolveExprValue(missing, vars); got != "{nope}" {
		t.Fatalf("unknown ref: got %q, want verbatim", got)
	}

	// An empty trigger row is treated as absent (verbatim).
	if got := resolveExprValue(lit, map[string]any{TriggerRowVar: map[string]any{}}); got != lit.Value {
		t.Fatalf("empty trigger row: got %q, want verbatim", got)
	}
}

package tln

import (
	"testing"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/factstore"
)

// A `when` clause resolves a triggering-record field (e.g. `category`) from the
// record's row, not just the event's single fact.
func TestEvalWhen_RecordField(t *testing.T) {
	cond := &ast.CompareCondition{
		Left:  &ast.IdentExpr{Name: "category"},
		Op:    "==",
		Right: &ast.LiteralExpr{Value: "Monitors"},
	}
	ev := factstore.Event{}

	if pass, err := evalWhen(cond, ev, map[string]any{"category": "Monitors"}); err != nil || !pass {
		t.Fatalf("category==Monitors should pass: pass=%v err=%v", pass, err)
	}
	if pass, _ := evalWhen(cond, ev, map[string]any{"category": "Laptops"}); pass {
		t.Fatal("category==Monitors must not pass for Laptops")
	}
	if pass, _ := evalWhen(cond, ev, nil); pass {
		t.Fatal("with no row the record field is nil → must not pass")
	}
}

// Two on-blocks for the same trigger (distinguished by `when`) are a legitimate
// if/else — they share the auto-generated name but must not be rejected as a
// duplicate, since the dispatcher keys them by trigger + guard, not by name.
func TestCheck_DuplicateOnBlocksAllowed(t *testing.T) {
	src := `
on assert item { when category == "Monitors"  workflow "act" }
on assert item { when category != "Monitors"  workflow "act" }
workflow "act" {
  step "s" { tool "svc" "do" { arg "1" } }
}`
	if err := Check(src); err != nil {
		t.Fatalf("two `on assert item` branches should compile: %v", err)
	}
}

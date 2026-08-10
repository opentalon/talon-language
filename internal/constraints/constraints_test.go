package constraints

import (
	"strings"
	"testing"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/lexer"
	"github.com/opentalon/talon-language/internal/parser"
)

func parseConstraints(t *testing.T, src string) []*ast.ConstraintBlock {
	t.Helper()
	tokens, ld := lexer.Lex("test.tln", src)
	if ld.HasErrors() {
		t.Fatalf("lex: %v", ld)
	}
	prog, pd := parser.Parse("test.tln", tokens)
	if pd.HasErrors() {
		t.Fatalf("parse: %v", pd)
	}
	var out []*ast.ConstraintBlock
	for _, b := range prog.Blocks {
		if cb, ok := b.(*ast.ConstraintBlock); ok {
			out = append(out, cb)
		}
	}
	return out
}

func TestConstraintAcceptsValidRecord(t *testing.T) {
	cs := parseConstraints(t, `
constraint "Stock cannot be negative" {
  for records where type == "stock_item"
  require attr "current_stock" >= 0
  on_violation reject "stock must be non-negative"
}`)
	v := Check(map[string]any{
		"type":          "stock_item",
		"current_stock": 5.0,
	}, cs)
	if v.Mode != "accept" {
		t.Errorf("expected accept, got %v", v)
	}
}

func TestConstraintRejectsViolator(t *testing.T) {
	cs := parseConstraints(t, `
constraint "Stock cannot be negative" {
  for records where type == "stock_item"
  require attr "current_stock" >= 0
  on_violation reject "stock must be non-negative"
}`)
	v := Check(map[string]any{
		"type":          "stock_item",
		"current_stock": -3.0,
	}, cs)
	if v.Mode != "reject" {
		t.Errorf("expected reject, got %v", v)
	}
	if len(v.Reasons) == 0 || !strings.Contains(v.Reasons[0], "non-negative") {
		t.Errorf("expected violation message, got %v", v.Reasons)
	}
}

func TestConstraintSelectorSkipsNonMatching(t *testing.T) {
	cs := parseConstraints(t, `
constraint "Stock cannot be negative" {
  for records where type == "stock_item"
  require attr "current_stock" >= 0
  on_violation reject "stock must be non-negative"
}`)
	// type != stock_item — selector doesn't match, constraint doesn't apply.
	v := Check(map[string]any{
		"type":          "item",
		"current_stock": -3.0,
	}, cs)
	if v.Mode != "accept" {
		t.Errorf("expected accept (selector miss), got %v", v)
	}
}

func TestConstraintMembershipRejectsTypo(t *testing.T) {
	cs := parseConstraints(t, `
constraint "Item status is valid" {
  for records where type == "item"
  require attr "status" in ["active", "defective", "missing", "inactive"]
  on_violation reject "invalid status"
}`)
	v := Check(map[string]any{
		"type":   "item",
		"status": "actvie", // typo
	}, cs)
	if v.Mode != "reject" {
		t.Errorf("expected reject for typo'd status, got %v", v)
	}
}

func TestConstraintWarnMode(t *testing.T) {
	cs := parseConstraints(t, `
constraint "Stock should be non-negative" {
  for records where type == "stock_item"
  require attr "current_stock" >= 0
  on_violation warn "stock is negative"
}`)
	v := Check(map[string]any{
		"type":          "stock_item",
		"current_stock": -1.0,
	}, cs)
	if v.Mode != "warn" {
		t.Errorf("expected warn, got %v", v)
	}
}

func TestConstraintQuarantineMode(t *testing.T) {
	cs := parseConstraints(t, `
constraint "Tag suspicious" {
  for records where type == "item"
  require attr "stock" >= 0
  on_violation quarantine "needs review"
}`)
	v := Check(map[string]any{
		"type":  "item",
		"stock": -5.0,
	}, cs)
	if v.Mode != "quarantine" {
		t.Errorf("expected quarantine, got %v", v)
	}
}

func TestConstraintMostSevereWins(t *testing.T) {
	cs := parseConstraints(t, `
constraint "warn-only" {
  for records where type == "item"
  require attr "stock" >= 0
  on_violation warn "negative stock"
}

constraint "reject-it" {
  for records where type == "item"
  require attr "stock" >= -10
  on_violation reject "way too negative"
}`)
	// stock=-20 violates both; reject wins over warn.
	v := Check(map[string]any{
		"type":  "item",
		"stock": -20.0,
	}, cs)
	if v.Mode != "reject" {
		t.Errorf("expected reject (most-severe wins), got %v", v)
	}
	if len(v.Reasons) != 2 {
		t.Errorf("expected both reasons collected, got %v", v.Reasons)
	}
}

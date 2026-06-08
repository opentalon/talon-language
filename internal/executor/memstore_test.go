package executor

import (
	"context"
	"strings"
	"testing"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/factstore"
	"github.com/opentalon/talon-language/internal/lexer"
	"github.com/opentalon/talon-language/internal/parser"
	"github.com/opentalon/talon-language/internal/planner"
	"github.com/opentalon/talon-language/internal/validator"
)

// TestExecutor_MemoryStoreEndToEnd proves the FactStore abstraction by
// running a real Talon program through compile + execute against a
// MemoryStore (no Datalevin sidecar required). Mirrors the contract the
// CLI's `--store memory` flag depends on; a regression here would mean a
// new clause type was added to the planner without a matching case in
// MemoryStore's evaluator.
func TestExecutor_MemoryStoreEndToEnd(t *testing.T) {
	src := `
detect "Cement low" {
  for records where type == "stock_item"
    and attr "current_stock" <= attr "minimum_amount"
  flag matching items
  label "{item.name} low"
  priority HIGH
}`
	tokens, ld := lexer.Lex("test.talon", src)
	if ld.HasErrors() {
		t.Fatalf("lex: %v", ld)
	}
	prog, pd := parser.Parse("test.talon", tokens)
	if pd.HasErrors() {
		t.Fatalf("parse: %v", pd)
	}
	if vd := validator.Validate("test.talon", prog); vd.HasErrors() {
		t.Fatalf("validate: %v", vd)
	}
	plans, planDiags := planner.Plan(prog)
	if planDiags.HasErrors() {
		t.Fatalf("plan: %v", planDiags)
	}

	store := factstore.NewMemoryStore()
	if _, err := (&Executor{Client: store}).Seed(context.Background(), &ast.Program{
		Blocks: []ast.Block{&ast.TestBlock{
			Given: []ast.TestDatum{
				{Kind: "record", ID: 808, Fields: map[string]interface{}{"type": "stock_item"}},
				{Kind: "attr", ID: 808, Fields: map[string]interface{}{"name": "Portland Cement"}},
				{Kind: "attr", ID: 808, Fields: map[string]interface{}{"current_stock": 12.0}},
				{Kind: "attr", ID: 808, Fields: map[string]interface{}{"minimum_amount": 50.0}},
				{Kind: "record", ID: 809, Fields: map[string]interface{}{"type": "stock_item"}},
				{Kind: "attr", ID: 809, Fields: map[string]interface{}{"name": "Rebar"}},
				{Kind: "attr", ID: 809, Fields: map[string]interface{}{"current_stock": 200.0}},
				{Kind: "attr", ID: 809, Fields: map[string]interface{}{"minimum_amount": 50.0}},
			},
		}},
	}); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	exec := &Executor{Client: store}
	result, err := exec.Run(context.Background(), plans["Cement low"])
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Flagged) != 1 {
		t.Fatalf("want 1 flagged row (808 Portland Cement), got %d (%v)", len(result.Flagged), result.Flagged)
	}
	// First column of a FactQuery row is the entity ID, as float64 (the
	// MemoryStore's binding type for "?e").
	if id, ok := result.Flagged[0][0].(float64); !ok || int(id) != 808 {
		t.Errorf("want entity 808, got %v", result.Flagged[0])
	}
}

// TestExecutor_MemoryStore_NotInMembership exercises the structured `in`
// predicate end-to-end. The planner emits a "not_in" predicate for
// negated membership; this confirms MemoryStore's renderer + evaluator
// agree on the meaning.
func TestExecutor_MemoryStore_NotInMembership(t *testing.T) {
	src := `
detect "Active items" {
  for records where type == "item"
    and status not in ["archived", "deleted"]
  flag matching items
}`
	prog := mustParseProg(t, src)
	plans, _ := planner.Plan(prog)

	store := factstore.NewMemoryStore()
	mustAssert(t, store, []factstore.Fact{
		{RecordID: "1", Attribute: ":record/type", Value: "item"},
		{RecordID: "1", Attribute: ":record/status", Value: "active"},
		{RecordID: "2", Attribute: ":record/type", Value: "item"},
		{RecordID: "2", Attribute: ":record/status", Value: "archived"},
	})

	result, err := (&Executor{Client: store}).Run(context.Background(), plans["Active items"])
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Flagged) != 1 {
		t.Errorf("want 1 row (1, active), got %d", len(result.Flagged))
	}
}

func mustParseProg(t *testing.T, src string) *ast.Program {
	t.Helper()
	tokens, ld := lexer.Lex("t.talon", src)
	if ld.HasErrors() {
		t.Fatalf("lex: %v", ld)
	}
	prog, pd := parser.Parse("t.talon", tokens)
	if pd.HasErrors() {
		t.Fatalf("parse: %v", pd)
	}
	if vd := validator.Validate("t.talon", prog); vd.HasErrors() {
		var msgs []string
		for _, d := range vd {
			msgs = append(msgs, d.Message)
		}
		t.Fatalf("validate: %s", strings.Join(msgs, "; "))
	}
	return prog
}

func mustAssert(t *testing.T, store *factstore.MemoryStore, facts []factstore.Fact) {
	t.Helper()
	if err := store.Assert(context.Background(), facts); err != nil {
		t.Fatalf("Assert: %v", err)
	}
}

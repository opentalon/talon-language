package talon_test

import (
	"context"
	"testing"

	"github.com/opentalon/talon-language/pkg/talon"
)

func TestCheck_ValidWorkflow(t *testing.T) {
	src := `
workflow "ok" {
  step "one" {
    mcp "svc" "do" { arg "x" }
  }
}`
	if err := talon.Check(src); err != nil {
		t.Fatalf("Check on valid source: unexpected error: %v", err)
	}
}

func TestCheck_ParseError(t *testing.T) {
	src := `workflow "broken" { step "s1" {` // unterminated braces
	err := talon.Check(src)
	if err == nil {
		t.Fatal("expected a compile error")
	}
	ce, ok := err.(*talon.CompileError)
	if !ok {
		t.Fatalf("expected *CompileError, got %T: %v", err, err)
	}
	if ce.Stage != "parse" {
		t.Errorf("Stage: got %q, want parse", ce.Stage)
	}
	if len(ce.Diags) == 0 {
		t.Error("expected at least one diagnostic")
	}
}

func TestCheck_ValidationError_UnknownBlockReference(t *testing.T) {
	// Parses fine, but the recommend references a detect block that
	// does not exist — a validate-stage failure.
	src := `
recommend "act" {
  when detect "Nonexistent" matches
  suggest "never happens"
}`
	err := talon.Check(src)
	if err == nil {
		t.Fatal("expected a compile error")
	}
	ce, ok := err.(*talon.CompileError)
	if !ok {
		t.Fatalf("expected *CompileError, got %T: %v", err, err)
	}
	if ce.Stage != "validate" {
		t.Errorf("Stage: got %q, want validate", ce.Stage)
	}
}

func TestCheck_NoFactStoreNeeded_DetectBlock(t *testing.T) {
	// A detect block would need a FactStore to *run*, but Check only
	// compiles — so it must succeed with no store wired up.
	src := `
detect "Low stock" {
  for records where type == "stock_item"
    and attr "current_stock" < attr "minimum_amount"
  flag matching items
}`
	if err := talon.Check(src); err != nil {
		t.Fatalf("Check on detect-only source: unexpected error: %v", err)
	}
}

func TestCheck_FilenameOption(t *testing.T) {
	src := `workflow "broken" { step "s1" {`
	err := talon.Check(src, talon.WithFilename("agent.tln"))
	if err == nil {
		t.Fatal("expected a compile error")
	}
	if got := err.Error(); !contains(got, "agent.tln") {
		t.Errorf("error should reference the filename, got: %v", got)
	}
}

// TestFacts_UsableFromExternalModule proves the exported fact/event
// vocabulary lets outside code build facts and assert them into a store
// obtained through the public API — the create-time path opentalon-agents
// depends on.
func TestFacts_UsableFromExternalModule(t *testing.T) {
	store := talon.NewMemoryStore()

	var got talon.Event
	var fired bool
	store.Events().Subscribe(func(_ context.Context, ev talon.Event) {
		got = ev
		fired = true
	})

	facts := []talon.Fact{
		{RecordID: "1", Attribute: "current_stock", Value: 3},
	}
	if err := store.Assert(context.Background(), facts); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if !fired {
		t.Fatal("expected an event on first assert")
	}
	if got.Kind != talon.EventAssert {
		t.Errorf("Kind: got %v, want EventAssert", got.Kind)
	}

	// A value change on the same cell fires EventChange.
	fired = false
	if err := store.Assert(context.Background(), []talon.Fact{
		{RecordID: "1", Attribute: "current_stock", Value: 0},
	}); err != nil {
		t.Fatalf("Assert (change): %v", err)
	}
	if !fired || got.Kind != talon.EventChange {
		t.Errorf("expected EventChange, got fired=%v kind=%v", fired, got.Kind)
	}

	// Retract via the exported RetractPattern type.
	if err := store.Retract(context.Background(), talon.RetractPattern{RecordID: "1"}); err != nil {
		t.Fatalf("Retract: %v", err)
	}
	if got.Kind != talon.EventRetract {
		t.Errorf("Kind after retract: got %v, want EventRetract", got.Kind)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

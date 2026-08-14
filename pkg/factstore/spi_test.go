package factstore_test

// This test stands in for an out-of-tree backend (e.g. tln-db): it implements
// FactStore using ONLY the public pkg/factstore surface — no internal imports —
// and inspects a Query by type-switching over its clauses. If this compiles,
// an external module can implement the contract with no adapter shim.

import (
	"context"
	"testing"

	fs "github.com/opentalon/tln-language/pkg/factstore"
	"github.com/opentalon/tln-language/pkg/tln"
)

// extBackend is a trivial backend built entirely from the public SPI.
type extBackend struct{ facts []fs.Fact }

func (b *extBackend) Query(_ context.Context, q fs.Query) ([][]any, error) {
	// A real backend translates the clause tree to its wire protocol. Here we
	// just prove the AST is fully inspectable through the public types.
	var rows [][]any
	for _, c := range q.Where {
		switch cl := c.(type) {
		case *fs.Pattern:
			rows = append(rows, []any{"pattern", cl.Attribute})
		case *fs.Predicate:
			rows = append(rows, []any{"predicate", cl.Op})
		case *fs.Or, *fs.Not, *fs.RuleCall, *fs.FullText:
			rows = append(rows, []any{"composite"})
		}
	}
	return rows, nil
}

func (b *extBackend) Assert(_ context.Context, facts []fs.Fact) error {
	b.facts = append(b.facts, facts...)
	return nil
}

func (b *extBackend) Retract(_ context.Context, _ fs.RetractPattern) error { return nil }

// Compile-time proof: the external type satisfies both the SPI interface and
// the tln SDK's FactStore (they are the same underlying type).
var (
	_ fs.FactStore  = (*extBackend)(nil)
	_ tln.FactStore = (*extBackend)(nil)
)

func TestExternalBackendImplementsFactStore(t *testing.T) {
	var b fs.FactStore = &extBackend{}
	if err := b.Assert(context.Background(), []fs.Fact{
		{RecordID: "1", Attribute: ":record/type", Value: "vehicle"},
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	// Build a Query with the public constructors + clause types, exactly as a
	// backend's own tests would, and inspect it.
	q := fs.Query{
		Find: []string{"?e"},
		Where: []fs.Clause{
			&fs.Pattern{Entity: fs.Var("e"), Attribute: ":record/type", Value: fs.Lit("vehicle")},
			&fs.Predicate{Op: ">", Left: fs.Var("km"), Right: fs.Lit(50000)},
		},
	}
	rows, err := b.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 2 || rows[0][0] != "pattern" || rows[1][0] != "predicate" {
		t.Fatalf("clause inspection through the SPI failed: %v", rows)
	}
}

// TestExternalRuleSetWithNegation proves an out-of-tree solver (e.g. tln-asp)
// can build a negation-bearing rule set from the public SPI alone — a Rule
// whose body carries a Negation clause. This is what ASP's `head :- body, not q`
// needs at the boundary.
func TestExternalRuleSetWithNegation(t *testing.T) {
	// win(X) :- move(X,Y), not win(Y)  — the canonical negation-through-recursion.
	rules := []fs.Rule{{
		Name: "win",
		Args: []string{"?x"},
		Body: []fs.Clause{
			&fs.Pattern{Entity: fs.Var("e"), Attribute: ":edge/from", Value: fs.Var("x")},
			&fs.Pattern{Entity: fs.Var("e"), Attribute: ":edge/to", Value: fs.Var("y")},
			&fs.Negation{Name: "win", Args: []fs.Term{fs.Var("y")}},
		},
	}}
	// The Negation is a Clause, inspectable via a type switch — exactly how a
	// solver splits positive vs negative body literals.
	var neg int
	for _, c := range rules[0].Body {
		if _, ok := c.(*fs.Negation); ok {
			neg++
		}
	}
	if neg != 1 {
		t.Fatalf("expected 1 Negation clause reachable through the SPI, got %d", neg)
	}
}

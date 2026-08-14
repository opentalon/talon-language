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

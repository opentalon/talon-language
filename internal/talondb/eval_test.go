package talondb

import (
	"context"
	"testing"

	"github.com/opentalon/tln-language/internal/factstore"
)

func seedFleet(t *testing.T) *Adapter {
	t.Helper()
	a, _ := newTestAdapter()
	if err := a.Assert(context.Background(), []factstore.Fact{
		{RecordID: "501", Attribute: ":record/type", Value: "item"},
		{RecordID: "501", Attribute: ":record/status", Value: "active"},
		{RecordID: "501", Attribute: ":attr/km", Value: 45000.0},
		{RecordID: "502", Attribute: ":record/type", Value: "item"},
		{RecordID: "502", Attribute: ":record/status", Value: "scheduled"},
		{RecordID: "502", Attribute: ":attr/km", Value: 10000.0},
		{RecordID: "503", Attribute: ":record/type", Value: "item"},
		{RecordID: "503", Attribute: ":record/status", Value: "retired"},
		{RecordID: "503", Attribute: ":attr/km", Value: 99999.0},
		{RecordID: "601", Attribute: ":record/type", Value: "category"},
		{RecordID: "601", Attribute: ":record/name", Value: "Vehicles"},
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	return a
}

// ---------- Or ----------

func TestAdapterQueryOrUnionsBranches(t *testing.T) {
	t.Parallel()
	a := seedFleet(t)
	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Or{Branches: [][]factstore.Clause{
				{&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/status", Value: factstore.Term{Literal: "active"}}},
				{&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/status", Value: factstore.Term{Literal: "scheduled"}}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (active + scheduled), got %d: %v", len(rows), rows)
	}
}

func TestAdapterQueryOrNoBranchMatches(t *testing.T) {
	t.Parallel()
	a := seedFleet(t)
	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Or{Branches: [][]factstore.Clause{
				{&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/status", Value: factstore.Term{Literal: "decommissioned"}}},
				{&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/status", Value: factstore.Term{Literal: "stolen"}}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows, got %d: %v", len(rows), rows)
	}
}

func TestAdapterQueryOrNested(t *testing.T) {
	t.Parallel()
	a := seedFleet(t)
	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Or{Branches: [][]factstore.Clause{
				{&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/status", Value: factstore.Term{Literal: "active"}}},
				{
					&factstore.Or{Branches: [][]factstore.Clause{
						{&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/status", Value: factstore.Term{Literal: "scheduled"}}},
						{&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/status", Value: factstore.Term{Literal: "retired"}}},
					}},
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows (all items), got %d: %v", len(rows), rows)
	}
}

func TestAdapterQueryOrPredicateInsideBranch(t *testing.T) {
	t.Parallel()
	a := seedFleet(t)
	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e", "?km"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":attr/km", Value: factstore.Var("km")},
			&factstore.Or{Branches: [][]factstore.Clause{
				{&factstore.Predicate{Op: "<", Left: factstore.Var("km"), Right: factstore.Term{Literal: 20000.0}}},
				{&factstore.Predicate{Op: ">", Left: factstore.Var("km"), Right: factstore.Term{Literal: 50000.0}}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// 502 (10000 < 20000) + 503 (99999 > 50000) = 2; 501 (45000) is in the middle, excluded.
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(rows), rows)
	}
}

// ---------- Not ----------

func TestAdapterQueryNotExcludes(t *testing.T) {
	t.Parallel()
	a := seedFleet(t)
	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Not{Body: []factstore.Clause{
				&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/status", Value: factstore.Term{Literal: "retired"}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// 501 (active) + 502 (scheduled) = 2; 503 (retired) excluded.
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(rows), rows)
	}
}

func TestAdapterQueryNotDoesNotLeakBindings(t *testing.T) {
	t.Parallel()
	a := seedFleet(t)
	// Predicate uses ?status which was bound only inside the Not body
	// (and Not produces no bindings, so the predicate sees nil).
	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Not{Body: []factstore.Clause{
				&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/status", Value: factstore.Var("status")},
				&factstore.Predicate{Op: "==", Left: factstore.Var("status"), Right: factstore.Term{Literal: "retired"}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// Only 503 matches the inner pattern set, so Not(=) excludes it.
	// 501 + 502 remain.
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(rows), rows)
	}
}

// ---------- FullText ----------

func TestAdapterQueryFullTextSubstring(t *testing.T) {
	t.Parallel()
	a := seedFleet(t)
	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "category"}},
			&factstore.FullText{Entity: factstore.Var("e"), Query: "vehic"},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (Vehicles), got %d: %v", len(rows), rows)
	}
}

func TestAdapterQueryFullTextAttributeScoped(t *testing.T) {
	t.Parallel()
	a := seedFleet(t)
	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "category"}},
			&factstore.FullText{Entity: factstore.Var("e"), Attribute: ":record/name", Query: "Vehicles"},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
}

func TestAdapterQueryFullTextNoMatch(t *testing.T) {
	t.Parallel()
	a := seedFleet(t)
	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "category"}},
			&factstore.FullText{Entity: factstore.Var("e"), Query: "xyzzy"},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(rows))
	}
}

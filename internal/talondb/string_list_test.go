package talondb

import (
	"context"
	"testing"

	"github.com/opentalon/tln-language/internal/factstore"
)

func seedListPRs(t *testing.T) *Adapter {
	t.Helper()
	a, _ := newTestAdapter()
	if err := a.Assert(context.Background(), []factstore.Fact{
		{RecordID: "601", Attribute: ":record/type", Value: "pr"},
		{RecordID: "601", Attribute: ":attr/changed_files", Value: []any{"go.mod", "main.go"}},
		{RecordID: "602", Attribute: ":record/type", Value: "pr"},
		{RecordID: "602", Attribute: ":attr/changed_files", Value: "go.mod,main.go"},
		{RecordID: "603", Attribute: ":record/type", Value: "pr"},
		{RecordID: "603", Attribute: ":attr/changed_files", Value: []any{"README.md"}},
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	return a
}

func queryPRIDs(t *testing.T, a *Adapter, extra factstore.Clause) []float64 {
	t.Helper()
	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "pr"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":attr/changed_files", Value: factstore.Var("f")},
			extra,
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	out := make([]float64, 0, len(rows))
	for _, row := range rows {
		id, ok := row[0].(float64)
		if !ok {
			t.Fatalf("entity id is %T, want float64", row[0])
		}
		out = append(out, id)
	}
	return out
}

// Issue #158: string predicates quantify over list-valued attributes on the
// adapter's Go-side evaluator too, not just MemoryStore.
func TestAdapterStringPredicateQuantifiesOverList(t *testing.T) {
	t.Parallel()
	a := seedListPRs(t)
	cases := []struct {
		op     string
		needle any
		want   []float64
	}{
		{"contains", "go.mod", []float64{601, 602}},
		{"starts_with", "go.", []float64{601, 602}},
		{"ends_with", ".md", []float64{603}},
		{"contains", "nowhere", nil},
		{"contains", 42.0, nil},
		// `==` stays strict against a list.
		{"==", "go.mod", nil},
	}
	for _, c := range cases {
		got := queryPRIDs(t, a, &factstore.Predicate{
			Op:    c.op,
			Left:  factstore.Var("f"),
			Right: factstore.Term{Literal: c.needle},
		})
		if !sameEntityIDs(got, c.want) {
			t.Errorf("%s %#v = %v, want %v", c.op, c.needle, got, c.want)
		}
	}
}

func TestAdapterFullTextScansListElements(t *testing.T) {
	t.Parallel()
	a := seedListPRs(t)
	cases := []struct {
		query string
		want  []float64
	}{
		{"main.go", []float64{601, 602}},
		{"README", []float64{603}},
		{"nowhere", nil},
	}
	for _, c := range cases {
		got := queryPRIDs(t, a, &factstore.FullText{
			Entity:    factstore.Var("e"),
			Attribute: ":attr/changed_files",
			Query:     c.query,
		})
		if !sameEntityIDs(got, c.want) {
			t.Errorf("fulltext %q = %v, want %v", c.query, got, c.want)
		}
	}
}

func sameEntityIDs(got, want []float64) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[float64]int{}
	for _, g := range got {
		seen[g]++
	}
	for _, w := range want {
		seen[w]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

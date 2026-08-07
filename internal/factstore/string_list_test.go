package factstore

import (
	"context"
	"testing"
)

// String predicates against a list-valued attribute hold when any element
// satisfies them (issue #158). Entity 601 carries the list shape, 602 the
// joined-string shape that already worked.
func newListSeeded(t *testing.T) *MemoryStore {
	t.Helper()
	m := NewMemoryStore()
	if err := m.Assert(context.Background(), []Fact{
		{RecordID: "601", Attribute: ":record/type", Value: "pr"},
		{RecordID: "601", Attribute: ":attr/changed_files", Value: []any{"go.mod", "main.go"}},

		{RecordID: "602", Attribute: ":record/type", Value: "pr"},
		{RecordID: "602", Attribute: ":attr/changed_files", Value: "go.mod,main.go"},

		{RecordID: "603", Attribute: ":record/type", Value: "pr"},
		{RecordID: "603", Attribute: ":attr/changed_files", Value: []string{"README.md"}},

		{RecordID: "604", Attribute: ":record/type", Value: "pr"},
		{RecordID: "604", Attribute: ":attr/changed_files", Value: []any{}},

		{RecordID: "605", Attribute: ":record/type", Value: "pr"},
		{RecordID: "605", Attribute: ":attr/changed_files", Value: []any{42.0, nil}},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return m
}

func queryStringPredicate(t *testing.T, m *MemoryStore, op string, needle any) []float64 {
	t.Helper()
	q := Query{
		Find: []string{"?e"},
		Where: []Clause{
			&Pattern{Entity: Var("e"), Attribute: ":record/type", Value: Lit("pr")},
			&Pattern{Entity: Var("e"), Attribute: ":attr/changed_files", Value: Var("f")},
			&Predicate{Op: op, Left: Var("f"), Right: Lit(needle)},
		},
	}
	rows, err := m.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	out := make([]float64, 0, len(rows))
	for _, row := range rows {
		out = append(out, row[0].(float64))
	}
	return out
}

func TestStringPredicateQuantifiesOverList(t *testing.T) {
	m := newListSeeded(t)
	cases := []struct {
		op     string
		needle any
		want   []float64
	}{
		{"contains", "go.mod", []float64{601, 602}},
		{"starts_with", "go.", []float64{601, 602}},
		{"ends_with", ".go", []float64{601, 602}},
		{"contains", "README", []float64{603}},
		// Unhappy paths: nothing matches an empty list, a list with no string
		// elements, or a non-string needle.
		{"contains", "nowhere", nil},
		{"contains", 42.0, nil},
	}
	for _, c := range cases {
		got := queryStringPredicate(t, m, c.op, c.needle)
		if !sameIDs(got, c.want) {
			t.Errorf("%s %#v = %v, want %v", c.op, c.needle, got, c.want)
		}
	}
}

// `==` stays strict: a list-valued attribute does not equal one of its
// elements. Widening it is a separate decision (issue #158).
func TestEqualityStaysStrictAgainstList(t *testing.T) {
	m := newListSeeded(t)
	got := queryStringPredicate(t, m, "==", "go.mod")
	if len(got) != 0 {
		t.Errorf("== against list matched %v, want no rows", got)
	}
}

// Full text (`matches` / `matches_phrase`) scans list-valued attributes
// element by element, not only string-valued ones (issue #158).
func TestFullTextScansListElements(t *testing.T) {
	m := newListSeeded(t)
	cases := []struct {
		query string
		want  []float64
	}{
		{"main.go", []float64{601, 602}},
		{"README", []float64{603}},
		{"nowhere", nil},
	}
	for _, c := range cases {
		q := Query{
			Find: []string{"?e"},
			Where: []Clause{
				&Pattern{Entity: Var("e"), Attribute: ":record/type", Value: Lit("pr")},
				&FullText{Entity: Var("e"), Query: c.query},
			},
		}
		rows, err := m.Query(context.Background(), q)
		if err != nil {
			t.Fatalf("Query %q: %v", c.query, err)
		}
		got := make([]float64, 0, len(rows))
		for _, row := range rows {
			got = append(got, row[0].(float64))
		}
		if !sameIDs(got, c.want) {
			t.Errorf("fulltext %q = %v, want %v", c.query, got, c.want)
		}
	}
}

// An attribute-scoped FullText searches only that attribute — a hit on some
// other attribute of the same entity must not match.
func TestFullTextAttributeScoped(t *testing.T) {
	m := newListSeeded(t)
	q := Query{
		Find: []string{"?e"},
		Where: []Clause{
			&Pattern{Entity: Var("e"), Attribute: ":record/type", Value: Lit("pr")},
			&FullText{Entity: Var("e"), Attribute: ":attr/changed_files", Query: "pr"},
		},
	}
	rows, err := m.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// "pr" is the value of :record/type, not of :attr/changed_files.
	if len(rows) != 0 {
		t.Errorf("attribute-scoped fulltext leaked to other attributes: %v", rows)
	}

	q.Where[1] = &FullText{Entity: Var("e"), Attribute: ":attr/changed_files", Query: "main.go"}
	rows, err = m.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	got := make([]float64, 0, len(rows))
	for _, row := range rows {
		got = append(got, row[0].(float64))
	}
	if !sameIDs(got, []float64{601, 602}) {
		t.Errorf("attribute-scoped fulltext = %v, want [601 602]", got)
	}
}

func sameIDs(got, want []float64) bool {
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

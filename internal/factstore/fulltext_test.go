package factstore

import (
	"context"
	"strings"
	"testing"
)

func TestFullTextMatchesSubstring(t *testing.T) {
	m := newSeeded(t)
	q := Query{
		Find: []string{"?e"},
		Where: []Clause{
			&Pattern{Entity: Var("e"), Attribute: ":record/type", Value: Lit("item")},
			&FullText{Entity: Var("e"), Query: "Transit"},
		},
	}
	rows, err := m.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 || rows[0][0].(float64) != 502 {
		t.Fatalf("want [[502]], got %v", rows)
	}
}

func TestFullTextCaseInsensitive(t *testing.T) {
	m := newSeeded(t)
	q := Query{
		Find:  []string{"?e"},
		Where: []Clause{&FullText{Entity: Var("e"), Query: "transporter"}},
	}
	rows, err := m.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 || rows[0][0].(float64) != 501 {
		t.Fatalf("want [[501]], got %v", rows)
	}
}

func TestFullTextNoMatch(t *testing.T) {
	m := newSeeded(t)
	q := Query{
		Find:  []string{"?e"},
		Where: []Clause{&FullText{Entity: Var("e"), Query: "nothing-matches-this"}},
	}
	rows, err := m.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("want no rows, got %v", rows)
	}
}

func TestFullTextEmptyQueryMatchesNothing(t *testing.T) {
	m := newSeeded(t)
	q := Query{
		Find:  []string{"?e"},
		Where: []Clause{&FullText{Entity: Var("e"), Query: ""}},
	}
	rows, err := m.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("empty query should match nothing, got %v", rows)
	}
}

func TestFullTextRendersDatalevinPredicate(t *testing.T) {
	q := Query{
		Find:  []string{"?e"},
		Where: []Clause{&FullText{Entity: Var("e"), Query: "transit"}},
	}
	got := q.String()
	if !strings.Contains(got, `(fulltext $ "transit")`) {
		t.Fatalf("want fulltext predicate in Datalog output, got:\n%s", got)
	}
	if !strings.Contains(got, "[?e ?ft-a ?ft-v]") {
		t.Fatalf("want destructured FTS binding, got:\n%s", got)
	}
}

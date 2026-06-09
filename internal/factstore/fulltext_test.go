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
	if args := q.QueryArgs(); len(args) != 0 {
		t.Fatalf("literal Query should not produce args, got %v", args)
	}
}

func TestFullTextExprRendersViaInParameter(t *testing.T) {
	q := Query{
		Find: []string{"?e"},
		Where: []Clause{
			&FullText{Entity: Var("e"), Expr: `[:and {:phrase "fuel pump"}]`},
		},
	}
	got := q.String()
	if !strings.Contains(got, ":in $ ?fts-q-0") {
		t.Fatalf("Expr form must declare ?fts-q-0 in :in, got:\n%s", got)
	}
	if !strings.Contains(got, "(fulltext $ ?fts-q-0)") {
		t.Fatalf("Expr form must reference ?fts-q-0 in fulltext call, got:\n%s", got)
	}
	args := q.QueryArgs()
	if len(args) != 1 || args[0] != `[:and {:phrase "fuel pump"}]` {
		t.Fatalf("QueryArgs = %v, want one entry with the expr literal", args)
	}
}

func TestFullTextMixedExprAndLiteralAssignArgsInScanOrder(t *testing.T) {
	q := Query{
		Find: []string{"?e"},
		Where: []Clause{
			&FullText{Entity: Var("e"), Query: "plain"},
			&FullText{Entity: Var("e"), Expr: `[:and "alpha"]`},
			&FullText{Entity: Var("e"), Expr: `[:and "beta"]`},
		},
	}
	got := q.String()
	if !strings.Contains(got, ":in $ ?fts-q-0 ?fts-q-1") {
		t.Fatalf("expected both ?fts-q-0 and ?fts-q-1 in :in, got:\n%s", got)
	}
	args := q.QueryArgs()
	if len(args) != 2 || args[0] != `[:and "alpha"]` || args[1] != `[:and "beta"]` {
		t.Fatalf("QueryArgs = %v, want [alpha, beta]", args)
	}
}

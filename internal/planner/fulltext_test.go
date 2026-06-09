package planner

import (
	"strings"
	"testing"

	"github.com/opentalon/talon-language/internal/factstore"
)

func findFullText(q factstore.Query) *factstore.FullText {
	for _, c := range q.Where {
		if f, ok := c.(*factstore.FullText); ok {
			return f
		}
	}
	return nil
}

func TestPlanner_MatchesEmitsFullTextClause(t *testing.T) {
	src := `
detect "Items containing transit" {
  for records where type == "item"
    and attr "description" matches "transit"
  flag matching items
  label "{item.name}"
  priority HIGH
}
`
	plan := planBlock(t, src, "Items containing transit")
	q := queryStep(t, plan, 0)
	ft := findFullText(q.Query)
	if ft == nil {
		t.Fatalf("expected FullText clause in Where, got: %+v", q.Query.Where)
	}
	if ft.Query != "transit" {
		t.Errorf("FullText.Query = %q, want %q", ft.Query, "transit")
	}
	if ft.Attribute != ":attr/description" {
		t.Errorf("FullText.Attribute = %q, want :attr/description", ft.Attribute)
	}
	if ft.Entity.Var != "?e" {
		t.Errorf("FullText.Entity.Var = %q, want ?e", ft.Entity.Var)
	}
}

func TestPlanner_MatchesPhraseEmitsAndForm(t *testing.T) {
	src := `
detect "Logs mentioning fuel pump" {
  for records where type == "service_log"
    and attr "notes" matches phrase "fuel pump"
  flag matching items
  label "{item.name}"
  priority HIGH
}
`
	plan := planBlock(t, src, "Logs mentioning fuel pump")
	q := queryStep(t, plan, 0)
	ft := findFullText(q.Query)
	if ft == nil {
		t.Fatalf("expected FullText clause, got: %+v", q.Query.Where)
	}
	if ft.Query != "" {
		t.Errorf("FullText.Query should be empty for phrase form, got %q", ft.Query)
	}
	if !strings.Contains(ft.Expr, `:phrase`) || !strings.Contains(ft.Expr, "fuel pump") {
		t.Errorf("FullText.Expr missing :phrase / payload, got %q", ft.Expr)
	}
	if ft.Attribute != ":attr/notes" {
		t.Errorf("FullText.Attribute = %q, want :attr/notes", ft.Attribute)
	}
}

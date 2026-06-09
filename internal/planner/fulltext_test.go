package planner

import (
	"testing"

	"github.com/opentalon/talon-language/internal/factstore"
)

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
	var ft *factstore.FullText
	for _, c := range q.Query.Where {
		if f, ok := c.(*factstore.FullText); ok {
			ft = f
			break
		}
	}
	if ft == nil {
		t.Fatalf("expected FullText clause in Where, got: %+v", q.Query.Where)
	}
	if ft.Query != "transit" {
		t.Errorf("FullText.Query = %q, want %q", ft.Query, "transit")
	}
	if ft.Entity.Var != "?e" {
		t.Errorf("FullText.Entity.Var = %q, want ?e", ft.Entity.Var)
	}
}

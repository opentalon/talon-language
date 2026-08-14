package planner

import (
	"testing"

	"github.com/opentalon/tln-language/internal/factstore"
)

func TestPlanner_CategoryTreeEmitsRecursiveRule(t *testing.T) {
	src := `
detect "Tools subtree items" {
  for records where type == "item"
    and category in category_tree("Tools")
  flag matching items
  label "{item.name}"
  priority HIGH
}
`
	plan := planBlock(t, src, "Tools subtree items")
	q := queryStep(t, plan, 0)
	if len(q.Query.Rules) == 0 {
		t.Fatalf("expected Query.Rules to be populated, got empty")
	}
	var foundBase, foundStep bool
	for _, r := range q.Query.Rules {
		if r.Name != "category-in-tree" {
			t.Errorf("unexpected rule name %q", r.Name)
		}
		// Base body: [:category/name pattern, (= ?c ?root) predicate]
		if len(r.Body) == 2 {
			if _, p := r.Body[0].(*factstore.Pattern); p {
				if pr, ok := r.Body[1].(*factstore.Predicate); ok && pr.Op == "=" {
					foundBase = true
				}
			}
		}
		// Step body: [:category/name pattern, :category/parent pattern, recursive RuleCall]
		if len(r.Body) >= 3 {
			foundStep = true
		}
	}
	if !foundBase {
		t.Errorf("missing base `(= ?c ?root)` rule body")
	}
	if !foundStep {
		t.Errorf("missing recursive rule body")
	}
	// And a RuleCall referencing "Tools" must appear in Where.
	var ruleCall *factstore.RuleCall
	for _, c := range q.Query.Where {
		if r, ok := c.(*factstore.RuleCall); ok {
			ruleCall = r
			break
		}
	}
	if ruleCall == nil {
		t.Fatalf("Where missing RuleCall, got: %+v", q.Query.Where)
	}
	if ruleCall.Name != "category-in-tree" {
		t.Errorf("RuleCall.Name = %q", ruleCall.Name)
	}
	if len(ruleCall.Args) != 2 || ruleCall.Args[1].Literal != "Tools" {
		t.Errorf("RuleCall args = %+v", ruleCall.Args)
	}
}

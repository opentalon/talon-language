package factstore

import (
	"context"
	"strings"
	"testing"
)

func newCategoryStore(t *testing.T) *MemoryStore {
	t.Helper()
	m := NewMemoryStore()
	// Category tree: Tools → Power → Drill, Tools → Hand, Outdoors
	if err := m.Assert(context.Background(), []Fact{
		{RecordID: "10", Attribute: ":record/type", Value: "category"},
		{RecordID: "10", Attribute: ":category/name", Value: "Tools"},

		{RecordID: "11", Attribute: ":record/type", Value: "category"},
		{RecordID: "11", Attribute: ":category/name", Value: "Power"},
		{RecordID: "11", Attribute: ":category/parent", Value: "Tools"},

		{RecordID: "12", Attribute: ":record/type", Value: "category"},
		{RecordID: "12", Attribute: ":category/name", Value: "Drill"},
		{RecordID: "12", Attribute: ":category/parent", Value: "Power"},

		{RecordID: "13", Attribute: ":record/type", Value: "category"},
		{RecordID: "13", Attribute: ":category/name", Value: "Hand"},
		{RecordID: "13", Attribute: ":category/parent", Value: "Tools"},

		{RecordID: "14", Attribute: ":record/type", Value: "category"},
		{RecordID: "14", Attribute: ":category/name", Value: "Outdoors"},

		// Items
		{RecordID: "100", Attribute: ":record/type", Value: "item"},
		{RecordID: "100", Attribute: ":record/category", Value: "Drill"},
		{RecordID: "101", Attribute: ":record/type", Value: "item"},
		{RecordID: "101", Attribute: ":record/category", Value: "Hand"},
		{RecordID: "102", Attribute: ":record/type", Value: "item"},
		{RecordID: "102", Attribute: ":record/category", Value: "Outdoors"},
		{RecordID: "103", Attribute: ":record/type", Value: "item"},
		{RecordID: "103", Attribute: ":record/category", Value: "Tools"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return m
}

func categoryTreeRules() []Rule {
	return []Rule{
		{
			Name: "category-in-tree",
			Args: []string{"?c", "?root"},
			Body: []Clause{
				&Predicate{Op: "=", Left: Var("c"), Right: Var("root")},
			},
		},
		{
			Name: "category-in-tree",
			Args: []string{"?c", "?root"},
			Body: []Clause{
				&Pattern{Entity: Var("cent"), Attribute: ":record/type", Value: Lit("category")},
				&Pattern{Entity: Var("cent"), Attribute: ":category/name", Value: Var("c")},
				&Pattern{Entity: Var("cent"), Attribute: ":category/parent", Value: Var("p")},
				&RuleCall{Name: "category-in-tree", Args: []Term{Var("p"), Var("root")}},
			},
		},
	}
}

func TestRules_CategoryTreeRoot(t *testing.T) {
	m := newCategoryStore(t)
	q := Query{
		Find: []string{"?e"},
		Where: []Clause{
			&Pattern{Entity: Var("e"), Attribute: ":record/type", Value: Lit("item")},
			&Pattern{Entity: Var("e"), Attribute: ":record/category", Value: Var("cat")},
			&RuleCall{Name: "category-in-tree", Args: []Term{Var("cat"), Lit("Tools")}},
		},
		Rules: categoryTreeRules(),
	}
	rows, err := m.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	got := map[float64]bool{}
	for _, r := range rows {
		got[r[0].(float64)] = true
	}
	for _, want := range []float64{100, 101, 103} {
		if !got[want] {
			t.Errorf("expected item %v in Tools subtree, missing", want)
		}
	}
	if got[102] {
		t.Errorf("item 102 (Outdoors) should not appear under Tools")
	}
}

func TestRules_CategoryTreeLeaf(t *testing.T) {
	m := newCategoryStore(t)
	q := Query{
		Find: []string{"?e"},
		Where: []Clause{
			&Pattern{Entity: Var("e"), Attribute: ":record/type", Value: Lit("item")},
			&Pattern{Entity: Var("e"), Attribute: ":record/category", Value: Var("cat")},
			&RuleCall{Name: "category-in-tree", Args: []Term{Var("cat"), Lit("Drill")}},
		},
		Rules: categoryTreeRules(),
	}
	rows, err := m.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 || rows[0][0].(float64) != 100 {
		t.Fatalf("want [[100]], got %v", rows)
	}
}

func TestRules_RendersAsDatalogVector(t *testing.T) {
	q := Query{
		Find:  []string{"?e"},
		Where: []Clause{&RuleCall{Name: "r", Args: []Term{Var("e"), Lit("Tools")}}},
		Rules: []Rule{
			{Name: "r", Args: []string{"?e", "?root"}, Body: []Clause{
				&Pattern{Entity: Var("e"), Attribute: ":record/category", Value: Var("root")},
			}},
		},
	}
	if !strings.Contains(q.String(), ":in $ %") {
		t.Errorf("query missing rules-arity in :in, got:\n%s", q.String())
	}
	if !strings.Contains(q.String(), "(r ?e \"Tools\")") {
		t.Errorf("query missing rule call, got:\n%s", q.String())
	}
	rules := q.RulesString()
	if !strings.Contains(rules, "[(r ?e ?root)") {
		t.Errorf("rules string missing rule head, got:\n%s", rules)
	}
	if !strings.Contains(rules, ":record/category") {
		t.Errorf("rules string missing body clause, got:\n%s", rules)
	}
}

package factstore

import (
	"context"
	"strconv"
	"testing"
)

// wfModelFor builds the well-founded model for a rule set over a store.
func wfModelFor(t *testing.T, m *MemoryStore, rules []Rule) *wfModel {
	t.Helper()
	return newRuleCtx(m, rules).wellFounded()
}

func truthOf(model *wfModel, name string, args ...any) int {
	return model.truth[atomKey(name, args)]
}

// edgeStore builds a directed graph: each edge is an entity carrying
// :edge/from and :edge/to node labels.
func edgeStore(t *testing.T, edges [][2]string) *MemoryStore {
	t.Helper()
	m := NewMemoryStore()
	var facts []Fact
	for i, e := range edges {
		id := strconv.Itoa(i + 1)
		facts = append(facts,
			Fact{RecordID: id, Attribute: ":edge/from", Value: e[0]},
			Fact{RecordID: id, Attribute: ":edge/to", Value: e[1]},
		)
	}
	if err := m.Assert(context.Background(), facts); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return m
}

// winMoveRules is the canonical game rule: a position is winning if some move
// leads to a position that is NOT winning. Negation appears through recursion.
func winMoveRules() []Rule {
	return []Rule{{
		Name: "win",
		Args: []string{"?x"},
		Body: []Clause{
			&Pattern{Entity: Var("e"), Attribute: ":edge/from", Value: Var("x")},
			&Pattern{Entity: Var("e"), Attribute: ":edge/to", Value: Var("y")},
			&Negation{Name: "win", Args: []Term{Var("y")}},
		},
	}}
}

// A terminal position loses; its predecessor wins. Stratifies cleanly, so the
// model is two-valued (no undefined).
func TestWellFounded_WinMoveTerminal(t *testing.T) {
	// a -> b, b terminal. b loses, a wins.
	m := edgeStore(t, [][2]string{{"a", "b"}})
	model := wfModelFor(t, m, winMoveRules())

	if got := truthOf(model, "win", "a"); got != wfTrue {
		t.Errorf("win(a): want true(%d), got %d", wfTrue, got)
	}
	// b has no move, so win(b) is derivably false (no ground rule).
	if got := truthOf(model, "win", "b"); got != 0 {
		t.Errorf("win(b): want false(0), got %d", got)
	}
}

// A 2-cycle (a<->b) is a draw: neither position is a forced win or loss, so the
// well-founded model leaves both undefined. This is the case that distinguishes
// well-founded semantics from naive stratified evaluation.
func TestWellFounded_WinMoveDrawUndefined(t *testing.T) {
	m := edgeStore(t, [][2]string{{"a", "b"}, {"b", "a"}})
	model := wfModelFor(t, m, winMoveRules())

	if got := truthOf(model, "win", "a"); got != wfUndefined {
		t.Errorf("win(a): want undefined(%d), got %d", wfUndefined, got)
	}
	if got := truthOf(model, "win", "b"); got != wfUndefined {
		t.Errorf("win(b): want undefined(%d), got %d", wfUndefined, got)
	}
}

// A mix: c -> d (d terminal) gives a definite win at c and loss at d, while a
// separate 2-cycle stays undefined — the model is genuinely three-valued.
func TestWellFounded_MixedDefiniteAndUndefined(t *testing.T) {
	m := edgeStore(t, [][2]string{{"a", "b"}, {"b", "a"}, {"c", "d"}})
	model := wfModelFor(t, m, winMoveRules())

	if got := truthOf(model, "win", "c"); got != wfTrue {
		t.Errorf("win(c): want true, got %d", got)
	}
	if got := truthOf(model, "win", "d"); got != 0 {
		t.Errorf("win(d): want false, got %d", got)
	}
	if got := truthOf(model, "win", "a"); got != wfUndefined {
		t.Errorf("win(a): want undefined, got %d", got)
	}
}

// Stratified negation: blocked(x) holds for an item that is not exempt. exempt
// is a lower stratum (pattern-derived), so the model is two-valued.
func stratifiedRules() []Rule {
	return []Rule{
		{
			Name: "exempt",
			Args: []string{"?x"},
			Body: []Clause{
				&Pattern{Entity: Var("x"), Attribute: ":attr/exempt", Value: Lit(true)},
			},
		},
		{
			Name: "blocked",
			Args: []string{"?x"},
			Body: []Clause{
				&Pattern{Entity: Var("x"), Attribute: ":record/type", Value: Lit("item")},
				&Negation{Name: "exempt", Args: []Term{Var("x")}},
			},
		},
	}
}

func TestWellFounded_StratifiedNegation(t *testing.T) {
	m := NewMemoryStore()
	if err := m.Assert(context.Background(), []Fact{
		{RecordID: "100", Attribute: ":record/type", Value: "item"},
		{RecordID: "100", Attribute: ":attr/exempt", Value: true},
		{RecordID: "101", Attribute: ":record/type", Value: "item"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	model := wfModelFor(t, m, stratifiedRules())

	if got := truthOf(model, "exempt", float64(100)); got != wfTrue {
		t.Errorf("exempt(100): want true, got %d", got)
	}
	if got := truthOf(model, "blocked", float64(100)); got != 0 {
		t.Errorf("blocked(100): want false (it is exempt), got %d", got)
	}
	if got := truthOf(model, "blocked", float64(101)); got != wfTrue {
		t.Errorf("blocked(101): want true (not exempt), got %d", got)
	}
}

// End-to-end: a Query whose RuleCall targets a negation-bearing predicate is
// answered from the well-founded model — only definitely-true atoms come back.
func TestWellFounded_QueryReturnsTrueAtoms(t *testing.T) {
	m := NewMemoryStore()
	if err := m.Assert(context.Background(), []Fact{
		{RecordID: "100", Attribute: ":record/type", Value: "item"},
		{RecordID: "100", Attribute: ":attr/exempt", Value: true},
		{RecordID: "101", Attribute: ":record/type", Value: "item"},
		{RecordID: "102", Attribute: ":record/type", Value: "item"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// The query gates each entity (bound to the conventional ?e) on the
	// negation-bearing rule; wfResolve answers with the definitely-true atoms.
	q := Query{
		Find:  []string{"?e"},
		Where: []Clause{&RuleCall{Name: "blocked", Args: []Term{Var("e")}}},
		Rules: stratifiedRules(),
	}
	rows, err := m.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	got := map[float64]bool{}
	for _, r := range rows {
		got[r[0].(float64)] = true
	}
	if got[100] {
		t.Errorf("item 100 is exempt and must not be blocked")
	}
	for _, want := range []float64{101, 102} {
		if !got[want] {
			t.Errorf("item %v should be blocked (not exempt), missing", want)
		}
	}
}

// Double negation resolves to the positive: reachable(x) :- item(x),
// not blocked(x), and blocked(x) :- item(x), not exempt(x). An exempt item is
// not blocked, hence reachable.
func TestWellFounded_DoubleNegation(t *testing.T) {
	m := NewMemoryStore()
	if err := m.Assert(context.Background(), []Fact{
		{RecordID: "100", Attribute: ":record/type", Value: "item"},
		{RecordID: "100", Attribute: ":attr/exempt", Value: true},
		{RecordID: "101", Attribute: ":record/type", Value: "item"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rules := append(stratifiedRules(), Rule{
		Name: "reachable",
		Args: []string{"?x"},
		Body: []Clause{
			&Pattern{Entity: Var("x"), Attribute: ":record/type", Value: Lit("item")},
			&Negation{Name: "blocked", Args: []Term{Var("x")}},
		},
	})
	model := wfModelFor(t, m, rules)

	// 100 is exempt → not blocked → reachable.
	if got := truthOf(model, "reachable", float64(100)); got != wfTrue {
		t.Errorf("reachable(100): want true, got %d", got)
	}
	// 101 is not exempt → blocked → not reachable.
	if got := truthOf(model, "reachable", float64(101)); got != 0 {
		t.Errorf("reachable(101): want false, got %d", got)
	}
}

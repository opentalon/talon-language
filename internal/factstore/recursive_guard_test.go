package factstore

import (
	"context"
	"testing"
)

// chainStore seeds a linear graph 1→2→3→4→5 as edge entities carrying numeric
// :edge/from and :edge/to values.
func chainStore(t *testing.T) *MemoryStore {
	t.Helper()
	m := NewMemoryStore()
	edges := [][2]float64{{1, 2}, {2, 3}, {3, 4}, {4, 5}}
	var facts []Fact
	for i, e := range edges {
		id := []string{"1000", "1001", "1002", "1003"}[i]
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

// reachCappedRules is transitive reachability with a numeric ceiling enforced by
// a `<=` comparison GUARD inside the recursive body — the capability ADR 0010
// adds. Before the change the guard would dead-end the derivation; now it filters
// bound values and the fixpoint still terminates.
func reachCappedRules() []Rule {
	return []Rule{
		{ // base: a direct edge whose target is within the cap
			Name: "reach-capped",
			Args: []string{"?from", "?to", "?cap"},
			Body: []Clause{
				&Pattern{Entity: Var("ed"), Attribute: ":edge/from", Value: Var("from")},
				&Pattern{Entity: Var("ed"), Attribute: ":edge/to", Value: Var("to")},
				&Predicate{Op: "<=", Left: Var("to"), Right: Var("cap")},
			},
		},
		{ // recursive: step to a mid node within the cap, then continue
			Name: "reach-capped",
			Args: []string{"?from", "?to", "?cap"},
			Body: []Clause{
				&Pattern{Entity: Var("ed"), Attribute: ":edge/from", Value: Var("from")},
				&Pattern{Entity: Var("ed"), Attribute: ":edge/to", Value: Var("mid")},
				&Predicate{Op: "<=", Left: Var("mid"), Right: Var("cap")},
				&RuleCall{Name: "reach-capped", Args: []Term{Var("mid"), Var("to"), Var("cap")}},
			},
		},
	}
}

// reachTargets resolves reach-capped(1, ?to, cap) directly through the recursive
// resolver — the code path the guard lives in — and returns the set of ?to it
// derives. (A top-level RuleCall in Query is a per-entity membership filter, not
// a multi-answer generator, so the resolver is what we exercise here.)
func reachTargets(t *testing.T, m *MemoryStore, cap float64) map[float64]bool {
	t.Helper()
	rc := newRuleCtx(m, reachCappedRules())
	tuples := rc.resolve("reach-capped", []any{1.0, nil, cap})
	got := map[float64]bool{}
	for _, tup := range tuples {
		got[tup[1].(float64)] = true // column order is [?from, ?to, ?cap]
	}
	return got
}

// TestRecursiveComparisonGuard_Filters proves a `<=` guard inside a recursive
// rule body prunes the derivation to within the cap.
func TestRecursiveComparisonGuard_Filters(t *testing.T) {
	m := chainStore(t)

	capped := reachTargets(t, m, 3.0)
	for _, want := range []float64{2, 3} {
		if !capped[want] {
			t.Errorf("cap=3: expected %v reachable, missing (got %v)", want, capped)
		}
	}
	for _, no := range []float64{4, 5} {
		if capped[no] {
			t.Errorf("cap=3: %v is beyond the cap and must be pruned (got %v)", no, capped)
		}
	}
}

// TestRecursiveComparisonGuard_CapControlsResult raises the cap and confirms the
// guard — not some other pruning — is what bounded the result: the full chain
// {2,3,4,5} comes back.
func TestRecursiveComparisonGuard_CapControlsResult(t *testing.T) {
	m := chainStore(t)
	full := reachTargets(t, m, 10.0)
	for _, want := range []float64{2, 3, 4, 5} {
		if !full[want] {
			t.Errorf("cap=10: expected full chain reachable, missing %v (got %v)", want, full)
		}
	}
}

// TestComparisonGuard_WellFoundedPath exercises the same guard on the
// negation-bearing resolver (enumGen). A Negation clause forces the well-founded
// evaluator; the `<=` guard must still filter by weight there.
func TestComparisonGuard_WellFoundedPath(t *testing.T) {
	m := NewMemoryStore()
	if err := m.Assert(context.Background(), []Fact{
		{RecordID: "1", Attribute: ":edge/to", Value: "b"},
		{RecordID: "1", Attribute: ":edge/w", Value: 1.0},
		{RecordID: "2", Attribute: ":edge/to", Value: "c"},
		{RecordID: "2", Attribute: ":edge/w", Value: 3.0},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// reachable-light(?to) :- edge to ?to, edge weight ?w, ?w <= 1, not dead(?to).
	// `dead` has no rules, so the negation is trivially satisfied; its only job is
	// to route resolution through the well-founded path (enumGen).
	rules := []Rule{{
		Name: "reachable-light",
		Args: []string{"?to"},
		Body: []Clause{
			&Pattern{Entity: Var("e"), Attribute: ":edge/to", Value: Var("to")},
			&Pattern{Entity: Var("e"), Attribute: ":edge/w", Value: Var("w")},
			&Predicate{Op: "<=", Left: Var("w"), Right: Lit(1.0)},
			&Negation{Name: "dead", Args: []Term{Var("to")}},
		},
	}}
	model := wfModelFor(t, m, rules)

	if got := truthOf(model, "reachable-light", "b"); got != wfTrue {
		t.Errorf("reachable-light(b): weight 1 passes the guard, want true(%d), got %d", wfTrue, got)
	}
	if got := truthOf(model, "reachable-light", "c"); got == wfTrue {
		t.Errorf("reachable-light(c): weight 3 fails the guard and must not be derived, got true")
	}
}

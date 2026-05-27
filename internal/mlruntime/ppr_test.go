package mlruntime

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/opentalon/talon-language/internal/factstore"
)

func starGraph(center string, leaves ...string) *factstore.GraphSnapshot {
	triples := []factstore.FactTriple{}
	for _, l := range leaves {
		triples = append(triples, factstore.FactTriple{Entity: center, Attribute: "edge", Value: l})
		triples = append(triples, factstore.FactTriple{Entity: l, Attribute: "edge", Value: center})
	}
	return factstore.BuildSnapshotFromTriples(triples, 1, factstore.SnapshotOptions{})
}

func pathGraph(nodes ...string) *factstore.GraphSnapshot {
	triples := []factstore.FactTriple{}
	for i := 0; i < len(nodes)-1; i++ {
		triples = append(triples, factstore.FactTriple{Entity: nodes[i], Attribute: "edge", Value: nodes[i+1]})
		triples = append(triples, factstore.FactTriple{Entity: nodes[i+1], Attribute: "edge", Value: nodes[i]})
	}
	return factstore.BuildSnapshotFromTriples(triples, 1, factstore.SnapshotOptions{})
}

func resultByEntity(t *testing.T, results []Result, entity string) Result {
	t.Helper()
	for _, r := range results {
		if e, _ := r.Explanation.Inputs["entity"].(string); e == entity {
			return r
		}
	}
	t.Fatalf("no result for entity %q in %+v", entity, results)
	return Result{}
}

func runPPR(t *testing.T, snap *factstore.GraphSnapshot, params map[string]any) []Result {
	t.Helper()
	prim := NewPersonalizedPageRank()
	results, err := prim.Compute(context.Background(), Input{Params: paramsWithGraph(params, snap)})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	return results
}

func paramsWithGraph(p map[string]any, snap *factstore.GraphSnapshot) map[string]any {
	out := map[string]any{}
	for k, v := range p {
		out[k] = v
	}
	out["graph"] = snap
	return out
}

func TestPPRStarGraphSeedRecoversLeavesEqually(t *testing.T) {
	g := starGraph("center", "l1", "l2", "l3", "l4", "l5")
	results := runPPR(t, g, map[string]any{
		"seeds":  []string{"center"},
		"top_k":  10,
	})
	if len(results) != 5 {
		t.Fatalf("expected 5 leaves, got %d", len(results))
	}
	first := results[0].Value.(float64)
	for _, r := range results {
		v := r.Value.(float64)
		if math.Abs(v-first) > 1e-9 {
			t.Errorf("leaves should have equal score, got %v vs %v", first, v)
		}
	}
}

func TestPPRPathGraphDecaysWithDistance(t *testing.T) {
	g := pathGraph("n1", "n2", "n3", "n4", "n5")
	results := runPPR(t, g, map[string]any{
		"seeds":  []string{"n1"},
		"top_k":  10,
	})

	want := []string{"n2", "n3", "n4", "n5"}
	for i, e := range want {
		if entity, _ := results[i].Explanation.Inputs["entity"].(string); entity != e {
			t.Errorf("rank %d: got %q, want %q (results=%+v)", i+1, entity, e, results)
		}
	}
	// Scores must strictly decrease with distance.
	for i := 0; i < len(results)-1; i++ {
		a, b := results[i].Value.(float64), results[i+1].Value.(float64)
		if a <= b {
			t.Errorf("score at rank %d (%v) should exceed rank %d (%v)", i+1, a, i+2, b)
		}
	}
}

func TestPPRTwoComponentsIsolated(t *testing.T) {
	g := factstore.BuildSnapshotFromTriples([]factstore.FactTriple{
		{Entity: "a1", Attribute: "edge", Value: "a2"},
		{Entity: "a2", Attribute: "edge", Value: "a1"},
		{Entity: "b1", Attribute: "edge", Value: "b2"},
		{Entity: "b2", Attribute: "edge", Value: "b1"},
	}, 1, factstore.SnapshotOptions{})

	results := runPPR(t, g, map[string]any{"seeds": []string{"a1"}, "top_k": 10})

	for _, r := range results {
		entity := r.Explanation.Inputs["entity"].(string)
		if entity == "b1" || entity == "b2" {
			// Disconnected component should only receive the floor mass
			// (1-damping) * personalization[i], which is 0 here because
			// the personalization is concentrated on a1.
			if r.Value.(float64) > 1e-9 {
				t.Errorf("disconnected node %q got non-trivial mass %v", entity, r.Value)
			}
		}
	}
}

func TestPPRDeterministicAcrossRuns(t *testing.T) {
	g := pathGraph("n1", "n2", "n3", "n4")
	params := map[string]any{"seeds": []string{"n1"}, "top_k": 10}
	r1 := runPPR(t, g, params)
	r2 := runPPR(t, g, params)
	if !reflect.DeepEqual(r1, r2) {
		t.Errorf("PPR not deterministic across runs:\n  r1=%+v\n  r2=%+v", r1, r2)
	}
}

func TestPPRMultipleSeedsAverage(t *testing.T) {
	// Symmetric graph: two seeds at opposite ends → middle node ranked highest.
	g := pathGraph("a", "b", "c", "d", "e")
	results := runPPR(t, g, map[string]any{
		"seeds":  []string{"a", "e"},
		"top_k":  10,
	})
	// Top result should be c (the midpoint).
	if entity, _ := results[0].Explanation.Inputs["entity"].(string); entity != "c" {
		t.Errorf("top of symmetric two-seed walk: got %q, want %q (results=%+v)", entity, "c", results)
	}
}

func TestPPREmptySeedsError(t *testing.T) {
	g := pathGraph("a", "b", "c")
	prim := NewPersonalizedPageRank()
	_, err := prim.Compute(context.Background(), Input{Params: map[string]any{"graph": g, "seeds": []string{}}})
	if !errors.Is(err, ErrEmptySeeds) {
		t.Fatalf("expected ErrEmptySeeds, got %v", err)
	}
}

func TestPPRNoGraphError(t *testing.T) {
	prim := NewPersonalizedPageRank()
	_, err := prim.Compute(context.Background(), Input{Params: map[string]any{"seeds": []string{"x"}}})
	if !errors.Is(err, ErrNoGraph) {
		t.Fatalf("expected ErrNoGraph, got %v", err)
	}
}

func TestPPRSeedNotInGraph(t *testing.T) {
	g := pathGraph("a", "b")
	prim := NewPersonalizedPageRank()
	_, err := prim.Compute(context.Background(), Input{Params: map[string]any{
		"graph": g,
		"seeds": []string{"missing"},
	}})
	if !errors.Is(err, ErrSeedNotInGraph) {
		t.Fatalf("expected ErrSeedNotInGraph, got %v", err)
	}
}

func TestPPRConvergenceFlagWithSmallMaxIter(t *testing.T) {
	g := pathGraph("a", "b", "c", "d", "e")
	results := runPPR(t, g, map[string]any{
		"seeds":          []string{"a"},
		"max_iterations": 1,
		"tolerance":      1e-12,
	})
	if len(results) == 0 {
		t.Fatal("expected results even on non-convergence")
	}
	if conv, _ := results[0].Explanation.Inputs["converged"].(bool); conv {
		t.Errorf("converged flag should be false with max_iterations=1")
	}
}

func TestPPRInvalidDampingRejected(t *testing.T) {
	g := pathGraph("a", "b", "c")
	prim := NewPersonalizedPageRank()
	_, err := prim.Compute(context.Background(), Input{Params: map[string]any{
		"graph":   g,
		"seeds":   []string{"a"},
		"damping": 1.0,
	}})
	if !errors.Is(err, ErrInvalidDamping) {
		t.Fatalf("expected ErrInvalidDamping, got %v", err)
	}
}

func TestPPRIncludeSeedsFlag(t *testing.T) {
	g := pathGraph("a", "b", "c")
	results := runPPR(t, g, map[string]any{
		"seeds":         []string{"a"},
		"include_seeds": true,
		"top_k":         10,
	})
	found := false
	for _, r := range results {
		if r.Explanation.Inputs["entity"] == "a" {
			found = true
		}
	}
	if !found {
		t.Errorf("include_seeds=true should yield seed in output, got %+v", results)
	}
}

func TestPPRDampingLocalityShift(t *testing.T) {
	g := pathGraph("n1", "n2", "n3", "n4", "n5", "n6")
	low := runPPR(t, g, map[string]any{"seeds": []string{"n1"}, "damping": 0.5, "top_k": 10})
	high := runPPR(t, g, map[string]any{"seeds": []string{"n1"}, "damping": 0.95, "top_k": 10})

	// Lower damping → mass stays closer to the seed → score(n2)/score(n6)
	// ratio is larger (more local).
	lowRatio := resultByEntity(t, low, "n2").Value.(float64) / resultByEntity(t, low, "n6").Value.(float64)
	highRatio := resultByEntity(t, high, "n2").Value.(float64) / resultByEntity(t, high, "n6").Value.(float64)
	if lowRatio <= highRatio {
		t.Errorf("damping=0.5 ratio %v should exceed damping=0.95 ratio %v (more localised)", lowRatio, highRatio)
	}
}

func TestPPRDeterministicAcrossInsertionOrders(t *testing.T) {
	// Same edges, different triple insertion orders. Snapshot ordering
	// must be the source of truth — not insertion order, which is map-
	// iterated and effectively random.
	tA := []factstore.FactTriple{
		{Entity: "n3", Attribute: "edge", Value: "n4"},
		{Entity: "n1", Attribute: "edge", Value: "n2"},
		{Entity: "n2", Attribute: "edge", Value: "n3"},
		{Entity: "n4", Attribute: "edge", Value: "n3"},
		{Entity: "n2", Attribute: "edge", Value: "n1"},
		{Entity: "n3", Attribute: "edge", Value: "n2"},
	}
	tB := []factstore.FactTriple{
		{Entity: "n1", Attribute: "edge", Value: "n2"},
		{Entity: "n2", Attribute: "edge", Value: "n1"},
		{Entity: "n2", Attribute: "edge", Value: "n3"},
		{Entity: "n3", Attribute: "edge", Value: "n2"},
		{Entity: "n3", Attribute: "edge", Value: "n4"},
		{Entity: "n4", Attribute: "edge", Value: "n3"},
	}
	gA := factstore.BuildSnapshotFromTriples(tA, 1, factstore.SnapshotOptions{})
	gB := factstore.BuildSnapshotFromTriples(tB, 1, factstore.SnapshotOptions{})

	rA := runPPR(t, gA, map[string]any{"seeds": []string{"n1"}, "top_k": 10})
	rB := runPPR(t, gB, map[string]any{"seeds": []string{"n1"}, "top_k": 10})

	if len(rA) != len(rB) {
		t.Fatalf("result counts differ: %d vs %d", len(rA), len(rB))
	}
	for i := range rA {
		eA := rA[i].Explanation.Inputs["entity"].(string)
		eB := rB[i].Explanation.Inputs["entity"].(string)
		if eA != eB {
			t.Errorf("rank %d differs: %q vs %q", i+1, eA, eB)
		}
		if math.Abs(rA[i].Value.(float64)-rB[i].Value.(float64)) > 1e-12 {
			t.Errorf("rank %d score differs: %v vs %v", i+1, rA[i].Value, rB[i].Value)
		}
	}
}

func TestPPRSnapshotCache(t *testing.T) {
	prim := NewPersonalizedPageRank()
	g := pathGraph("a", "b", "c")
	prim.SnapshotForCache("k1", g)

	if _, ok := prim.CachedSnapshot("k1", g.Version); !ok {
		t.Errorf("expected cache hit on matching version")
	}
	if _, ok := prim.CachedSnapshot("k1", g.Version+1); ok {
		t.Errorf("expected cache miss on stale version")
	}
	if _, ok := prim.CachedSnapshot("missing", g.Version); ok {
		t.Errorf("expected cache miss on unknown key")
	}
}

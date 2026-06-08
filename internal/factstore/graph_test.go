package factstore

import (
	"sort"
	"testing"
)

func TestBuildSnapshotSharedAttributeEdges(t *testing.T) {
	// Four entities: A, B, C share category="x"; D is isolated.
	// Expect a triangle A-B-C and no edges to D.
	triples := []FactTriple{
		{Entity: "A", Attribute: "category", Value: "x"},
		{Entity: "B", Attribute: "category", Value: "x"},
		{Entity: "C", Attribute: "category", Value: "x"},
		{Entity: "D", Attribute: "category", Value: "y"},
	}
	g := BuildSnapshotFromTriples(triples, 1, SnapshotOptions{})

	if g.NodeCount() != 4 {
		t.Fatalf("nodes: got %d, want 4", g.NodeCount())
	}
	if g.EdgeCount() != 3 {
		t.Fatalf("edges: got %d, want 3 (triangle A-B-C)", g.EdgeCount())
	}

	dIdx := g.NodeIndex["D"]
	if len(g.EdgesFrom[dIdx]) != 0 {
		t.Errorf("D should be isolated, got neighbors %v", g.EdgesFrom[dIdx])
	}

	aIdx := g.NodeIndex["A"]
	if len(g.EdgesFrom[aIdx]) != 2 {
		t.Errorf("A should have 2 neighbors (B, C), got %d", len(g.EdgesFrom[aIdx]))
	}
}

func TestBuildSnapshotEntityValuedAttribute(t *testing.T) {
	// `assigned_to` points to another entity → direct edge.
	triples := []FactTriple{
		{Entity: "task1", Attribute: "assigned_to", Value: "alice"},
		{Entity: "task2", Attribute: "assigned_to", Value: "alice"},
		{Entity: "alice", Attribute: "role", Value: "engineer"},
	}
	g := BuildSnapshotFromTriples(triples, 1, SnapshotOptions{})

	// 3 nodes. Edges: task1-task2 (shared assigned_to=alice), task1-alice,
	// task2-alice. So 3 edges total.
	if g.NodeCount() != 3 {
		t.Fatalf("nodes: got %d, want 3", g.NodeCount())
	}
	if g.EdgeCount() != 3 {
		t.Fatalf("edges: got %d, want 3", g.EdgeCount())
	}
}

func TestBuildSnapshotDeterministicOrdering(t *testing.T) {
	// Same triples in two different orders must produce identical
	// EdgesFrom adjacency lists. Maps are randomized, so this catches
	// any leaked map iteration.
	tA := []FactTriple{
		{Entity: "z", Attribute: "k", Value: "v"},
		{Entity: "a", Attribute: "k", Value: "v"},
		{Entity: "m", Attribute: "k", Value: "v"},
	}
	tB := []FactTriple{
		{Entity: "m", Attribute: "k", Value: "v"},
		{Entity: "a", Attribute: "k", Value: "v"},
		{Entity: "z", Attribute: "k", Value: "v"},
	}
	g1 := BuildSnapshotFromTriples(tA, 1, SnapshotOptions{})
	g2 := BuildSnapshotFromTriples(tB, 1, SnapshotOptions{})

	if !equalStrings(g1.Nodes, g2.Nodes) {
		t.Fatalf("node order differs:\n  g1=%v\n  g2=%v", g1.Nodes, g2.Nodes)
	}
	for i := range g1.EdgesFrom {
		if !equalInts(g1.EdgesFrom[i], g2.EdgesFrom[i]) {
			t.Errorf("row %d differs:\n  g1=%v\n  g2=%v", i, g1.EdgesFrom[i], g2.EdgesFrom[i])
		}
	}
}

func TestBuildSnapshotHubCapSkipsLargeBucket(t *testing.T) {
	// 6 entities all share category=hub. With MaxBucketSize=4 the bucket
	// is skipped entirely, leaving the graph edgeless.
	triples := []FactTriple{}
	for _, e := range []string{"a", "b", "c", "d", "e", "f"} {
		triples = append(triples, FactTriple{Entity: e, Attribute: "category", Value: "hub"})
	}
	g := BuildSnapshotFromTriples(triples, 1, SnapshotOptions{MaxBucketSize: 4})

	if g.EdgeCount() != 0 {
		t.Errorf("hub bucket should have been skipped, got %d edges", g.EdgeCount())
	}
}

func TestBuildSnapshotAttributeFilters(t *testing.T) {
	triples := []FactTriple{
		{Entity: "a", Attribute: "category", Value: "x"},
		{Entity: "b", Attribute: "category", Value: "x"},
		{Entity: "a", Attribute: "noise", Value: "n"},
		{Entity: "b", Attribute: "noise", Value: "n"},
	}
	g := BuildSnapshotFromTriples(triples, 1, SnapshotOptions{IncludeAttrs: []string{"category"}})
	if g.EdgeCount() != 1 {
		t.Errorf("include filter: got %d edges, want 1", g.EdgeCount())
	}
	if len(g.Attributes) != 1 || g.Attributes[0] != "category" {
		t.Errorf("attributes: got %v, want [category]", g.Attributes)
	}

	g2 := BuildSnapshotFromTriples(triples, 1, SnapshotOptions{ExcludeAttrs: []string{"noise"}})
	if g2.EdgeCount() != 1 {
		t.Errorf("exclude filter: got %d edges, want 1", g2.EdgeCount())
	}
}

func TestBuildSnapshotNodesAreSorted(t *testing.T) {
	triples := []FactTriple{
		{Entity: "zebra", Attribute: "k", Value: "v"},
		{Entity: "ant", Attribute: "k", Value: "v"},
		{Entity: "monkey", Attribute: "k", Value: "v"},
	}
	g := BuildSnapshotFromTriples(triples, 1, SnapshotOptions{})
	sorted := make([]string, len(g.Nodes))
	copy(sorted, g.Nodes)
	sort.Strings(sorted)
	if !equalStrings(g.Nodes, sorted) {
		t.Errorf("nodes not sorted: %v", g.Nodes)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

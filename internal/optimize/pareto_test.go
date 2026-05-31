package optimize

import (
	"math"
	"testing"
)

// Two-objective hand fixture for the textbook NSGA-II case:
// minimize x, maximize y. The non-dominated set is {A, B, C}; D is dominated
// by A (cheaper x AND higher y).
//
//	A: x=0.5, y=40   — boundary (cheapest x)
//	B: x=0.8, y=90   — interior frontier point
//	C: x=1.2, y=95   — boundary (highest y)
//	D: x=1.0, y=50   — dominated by A and B
func twoObjFixture() ([]Individual, []Objective) {
	return []Individual{
			{EntityID: 101, Values: []float64{0.8, 90}},
			{EntityID: 102, Values: []float64{0.5, 40}},
			{EntityID: 103, Values: []float64{1.2, 95}},
			{EntityID: 104, Values: []float64{1.0, 50}},
		}, []Objective{
			{Name: "cost", Dir: Minimize},
			{Name: "urgency", Dir: Maximize},
		}
}

func TestPareto_FrontierMembership(t *testing.T) {
	in, objs := twoObjFixture()
	res, err := Pareto(in, objs)
	if err != nil {
		t.Fatalf("Pareto: %v", err)
	}

	want := map[int]bool{101: true, 102: true, 103: true}
	got := map[int]int{} // entityID → rank
	for _, s := range res.All {
		got[s.EntityID] = s.Rank
	}
	for id := range want {
		if got[id] != 0 {
			t.Errorf("entity %d: want rank 0, got %d", id, got[id])
		}
	}
	if got[104] == 0 {
		t.Errorf("entity 104 should be dominated (rank > 0), got rank 0")
	}
	if len(res.Frontier) != 3 {
		t.Errorf("frontier size: want 3, got %d", len(res.Frontier))
	}
}

func TestPareto_BoundaryCrowdingIsInf(t *testing.T) {
	in, objs := twoObjFixture()
	res, _ := Pareto(in, objs)

	// Boundary points (extremes on either objective) must have +Inf crowding.
	// In this fixture: 102 is cheapest x, 103 is highest y → both boundaries.
	for _, s := range res.All {
		if s.Rank != 0 {
			continue
		}
		switch s.EntityID {
		case 102, 103:
			if !math.IsInf(s.CrowdingDist, 1) {
				t.Errorf("entity %d (boundary): want +Inf crowding, got %v", s.EntityID, s.CrowdingDist)
			}
		case 101:
			if math.IsInf(s.CrowdingDist, 1) {
				t.Errorf("entity 101 (interior): want finite crowding, got +Inf")
			}
		}
	}
}

func TestPareto_DominatedCounts(t *testing.T) {
	in, objs := twoObjFixture()
	res, _ := Pareto(in, objs)

	got := map[int]Solution{}
	for _, s := range res.All {
		got[s.EntityID] = s
	}

	// D (104) is dominated by A (101) AND B... wait — re-check:
	//   A=(0.8, 90), B=(0.5, 40), D=(1.0, 50)
	//   A vs D: A.x=0.8 < D.x=1.0 AND A.y=90 > D.y=50 → A dominates D ✓
	//   B vs D: B.x=0.5 < D.x=1.0 BUT B.y=40 < D.y=50 → no domination
	// So D is dominated by A only.
	if got[104].DominatedCount != 1 {
		t.Errorf("entity 104 dominated-count: want 1, got %d", got[104].DominatedCount)
	}
	if got[101].DominatedCount != 0 {
		t.Errorf("entity 101 dominated-count: want 0 (frontier), got %d", got[101].DominatedCount)
	}
	if got[101].Dominates != 1 {
		t.Errorf("entity 101 dominates-count: want 1 (dominates 104), got %d", got[101].Dominates)
	}
}

func TestPareto_OrderingRankThenCrowding(t *testing.T) {
	in, objs := twoObjFixture()
	res, _ := Pareto(in, objs)

	// All rank-0 solutions come before rank-1; within rank, +Inf crowding first.
	for i := 1; i < len(res.All); i++ {
		if res.All[i].Rank < res.All[i-1].Rank {
			t.Errorf("All[%d].Rank=%d < All[%d].Rank=%d (must be non-decreasing)",
				i, res.All[i].Rank, i-1, res.All[i-1].Rank)
		}
	}
}

func TestPareto_EmptyInput(t *testing.T) {
	res, err := Pareto(nil, []Objective{{Name: "x", Dir: Minimize}})
	if err != nil {
		t.Fatalf("empty input should not error: %v", err)
	}
	if len(res.All) != 0 || len(res.Frontier) != 0 {
		t.Errorf("empty input should yield empty result, got All=%d Frontier=%d", len(res.All), len(res.Frontier))
	}
}

func TestPareto_ArityMismatch(t *testing.T) {
	in := []Individual{{EntityID: 1, Values: []float64{1.0}}}
	objs := []Objective{{Name: "x", Dir: Minimize}, {Name: "y", Dir: Maximize}}
	_, err := Pareto(in, objs)
	if err == nil {
		t.Fatal("expected error for arity mismatch, got nil")
	}
}

func TestDominates_SymmetricNonDomination(t *testing.T) {
	a := Individual{Values: []float64{1.0, 5.0}}
	b := Individual{Values: []float64{2.0, 10.0}}
	objs := []Objective{{Name: "x", Dir: Minimize}, {Name: "y", Dir: Maximize}}
	// a wins on x, b wins on y → neither dominates the other.
	if Dominates(a, b, objs) || Dominates(b, a, objs) {
		t.Errorf("a and b are non-dominated; got Dominates(a,b)=%v Dominates(b,a)=%v",
			Dominates(a, b, objs), Dominates(b, a, objs))
	}
}

func TestPareto_SingleObjectiveDegeneratesToSort(t *testing.T) {
	// With one objective NSGA-II should produce rank = strict-better-than-count.
	in := []Individual{
		{EntityID: 1, Values: []float64{3}},
		{EntityID: 2, Values: []float64{1}},
		{EntityID: 3, Values: []float64{2}},
	}
	objs := []Objective{{Name: "cost", Dir: Minimize}}
	res, _ := Pareto(in, objs)

	got := map[int]int{}
	for _, s := range res.All {
		got[s.EntityID] = s.Rank
	}
	want := map[int]int{2: 0, 3: 1, 1: 2}
	for id, r := range want {
		if got[id] != r {
			t.Errorf("entity %d: want rank %d, got %d", id, r, got[id])
		}
	}
}

package optimize

import (
	"math"
	"testing"
)

// TestACO_SquareFindsOptimum is the headline correctness test: 4 cities at
// the corners of a unit square. The optimal TSP tour traverses the perimeter
// (length 4) — every other permutation crosses the diagonal and is strictly
// longer (perimeter + 2(√2 - 1) ≈ 4.83). With a fixed seed the ACO must
// converge to the perimeter.
func TestACO_SquareFindsOptimum(t *testing.T) {
	xs := []float64{0, 1, 1, 0}
	ys := []float64{0, 0, 1, 1}
	dist := EuclideanDistanceMatrix(xs, ys)

	res := ACO(dist, ACOConfig{Ants: 10, Iterations: 50, Seed: 7})

	if math.Abs(res.Length-4.0) > 1e-9 {
		t.Errorf("optimal square tour length: want 4.0, got %v", res.Length)
	}
	if len(res.Tour) != 4 {
		t.Errorf("tour length: want 4 nodes, got %d", len(res.Tour))
	}
	// Tour should be {0,1,2,3} in some rotation/direction.
	if !isSquarePerimeter(res.Tour) {
		t.Errorf("tour does not traverse the square perimeter: %v", res.Tour)
	}
}

func TestACO_HistoryMonotonicallyImproves(t *testing.T) {
	// 6 random-but-fixed points; ACO best-so-far history must be non-increasing.
	xs := []float64{0, 2, 5, 8, 4, 1}
	ys := []float64{0, 3, 1, 6, 7, 8}
	dist := EuclideanDistanceMatrix(xs, ys)

	res := ACO(dist, ACOConfig{Ants: 15, Iterations: 30, Seed: 99})

	for i := 1; i < len(res.History); i++ {
		if res.History[i] > res.History[i-1]+1e-12 {
			t.Errorf("history[%d]=%v > history[%d]=%v — best-so-far must be monotone",
				i, res.History[i], i-1, res.History[i-1])
		}
	}
}

func TestACO_Deterministic(t *testing.T) {
	xs := []float64{0, 1, 2, 3, 1.5, 0.5}
	ys := []float64{0, 1, 0, 1, 2, 2}
	d := EuclideanDistanceMatrix(xs, ys)

	r1 := ACO(d, ACOConfig{Ants: 8, Iterations: 20, Seed: 42})
	r2 := ACO(d, ACOConfig{Ants: 8, Iterations: 20, Seed: 42})

	if math.Abs(r1.Length-r2.Length) > 1e-12 {
		t.Errorf("seed 42 not deterministic: %v vs %v", r1.Length, r2.Length)
	}
}

func TestACO_EmptyInputDoesNotPanic(t *testing.T) {
	res := ACO(nil, ACOConfig{})
	if len(res.Tour) != 0 || res.Length != 0 {
		t.Errorf("empty input: want zero result, got %+v", res)
	}
}

func TestACO_SingleNode(t *testing.T) {
	res := ACO([][]float64{{0}}, ACOConfig{Ants: 5, Iterations: 5, Seed: 1})
	if len(res.Tour) != 1 || res.Tour[0] != 0 {
		t.Errorf("single node: want tour [0], got %v", res.Tour)
	}
	if res.Length != 0 {
		t.Errorf("single node: want length 0, got %v", res.Length)
	}
}

// isSquarePerimeter checks whether a 4-node tour visits {0,1,2,3} in either
// clockwise or counter-clockwise order, starting from any rotation. The
// square corners (0,0), (1,0), (1,1), (0,1) form a valid perimeter only if
// consecutive nodes share an edge — never diagonally opposite.
func isSquarePerimeter(tour []int) bool {
	if len(tour) != 4 {
		return false
	}
	// Adjacent corner pairs on the unit square.
	edges := map[[2]int]bool{
		{0, 1}: true, {1, 0}: true,
		{1, 2}: true, {2, 1}: true,
		{2, 3}: true, {3, 2}: true,
		{3, 0}: true, {0, 3}: true,
	}
	for i := 0; i < 4; i++ {
		a, b := tour[i], tour[(i+1)%4]
		if !edges[[2]int{a, b}] {
			return false
		}
	}
	return true
}

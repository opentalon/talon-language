package optimize

import (
	"testing"
)

// TestGA_SeededKnapsack_RecoversOptimum is the GA's headline correctness
// test: a small 0/1 knapsack with a known optimal subset. The GA must find
// the optimal mask (or a member of the optimal set) within a fixed seed +
// generation budget.
//
// Items (value, weight):
//
//	item 0: (10, 5)
//	item 1: (40, 4)   ← in optimum
//	item 2: (30, 6)   ← in optimum
//	item 3: (50, 3)   ← in optimum
//	item 4: (35, 8)
//
// K = 3, weight budget = 13. The unique-by-objective optimum is {1, 2, 3}
// with total value 120, weight 13 (exactly meets budget).
func TestGA_SeededKnapsack_RecoversOptimum(t *testing.T) {
	values := []float64{10, 40, 30, 50, 35}
	weights := []float64{5, 4, 6, 3, 8}
	const k = 3
	const budget = 13.0

	objMaxValue := func(mask []bool) float64 {
		// Maximize value → negate so dominance treats it as "more is better"
		// when paired with our Maximize direction.
		sum := 0.0
		for i, v := range mask {
			if v {
				sum += values[i]
			}
		}
		return sum
	}
	conWeight := func(mask []bool) float64 {
		sum := 0.0
		for i, v := range mask {
			if v {
				sum += weights[i]
			}
		}
		if sum > budget {
			return sum - budget
		}
		return 0
	}

	prob := NewSubsetProblem(len(values), k,
		[]func([]bool) float64{objMaxValue},
		[]func([]bool) float64{conWeight},
	)

	res, stats, err := GA(prob, []Objective{{Name: "value", Dir: Maximize}}, GAConfig{
		PopulationSize: 60,
		Generations:    80,
		Seed:           42,
	})
	if err != nil {
		t.Fatalf("GA: %v", err)
	}
	if len(res.Frontier) == 0 {
		t.Fatal("frontier empty — GA failed to find any feasible solution")
	}

	// Best individual on the frontier — for single-objective max, the highest value.
	bestValue := 0.0
	var bestMask []bool
	for _, s := range res.Frontier {
		if s.Values[0] > bestValue {
			bestValue = s.Values[0]
			bestMask, _ = s.Row.([]bool)
		}
	}
	if bestValue != 120 {
		t.Errorf("best value: want 120, got %v (mask=%v)", bestValue, bestMask)
	}
	wantMask := []bool{false, true, true, true, false}
	if !maskEqual(bestMask, wantMask) {
		t.Errorf("best mask: want %v (items 1,2,3), got %v", wantMask, bestMask)
	}

	// Sanity-check the trace.
	if len(stats) != 80 {
		t.Errorf("stats: want 80 generations, got %d", len(stats))
	}
	if stats[len(stats)-1].FeasibleRatio < 0.5 {
		t.Errorf("final feasible ratio: want > 0.5, got %v", stats[len(stats)-1].FeasibleRatio)
	}
}

func TestGA_DeterministicUnderFixedSeed(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	weights := []float64{1, 1, 1, 1, 1, 1, 1, 1}

	mkProb := func() *SubsetProblem {
		return NewSubsetProblem(8, 3,
			[]func([]bool) float64{func(m []bool) float64 {
				s := 0.0
				for i, v := range m {
					if v {
						s += values[i]
					}
				}
				return s
			}},
			[]func([]bool) float64{func(m []bool) float64 {
				s := 0.0
				for i, v := range m {
					if v {
						s += weights[i]
					}
				}
				if s > 3 {
					return s - 3
				}
				return 0
			}},
		)
	}

	res1, _, _ := GA(mkProb(), []Objective{{Name: "value", Dir: Maximize}},
		GAConfig{PopulationSize: 20, Generations: 30, Seed: 99})
	res2, _, _ := GA(mkProb(), []Objective{{Name: "value", Dir: Maximize}},
		GAConfig{PopulationSize: 20, Generations: 30, Seed: 99})

	if len(res1.Frontier) != len(res2.Frontier) {
		t.Fatalf("seed 99 produced different frontier sizes: %d vs %d", len(res1.Frontier), len(res2.Frontier))
	}
	for i := range res1.Frontier {
		if res1.Frontier[i].Values[0] != res2.Frontier[i].Values[0] {
			t.Errorf("seed 99 not deterministic at frontier[%d]: %v vs %v",
				i, res1.Frontier[i].Values, res2.Frontier[i].Values)
		}
	}
}

func TestGA_MultiObjective_ParetoFrontierHasMultipleSolutions(t *testing.T) {
	// 6 items, pick 2 — minimize cost AND maximize urgency. With trade-offs
	// the frontier should contain more than one solution.
	costs := []float64{1, 2, 3, 4, 5, 6}
	urgency := []float64{10, 20, 30, 40, 50, 60}

	prob := NewSubsetProblem(6, 2,
		[]func([]bool) float64{
			func(m []bool) float64 { // cost: minimize → return raw sum, dir=Minimize
				s := 0.0
				for i, v := range m {
					if v {
						s += costs[i]
					}
				}
				return s
			},
			func(m []bool) float64 { // urgency: maximize → return raw sum, dir=Maximize
				s := 0.0
				for i, v := range m {
					if v {
						s += urgency[i]
					}
				}
				return s
			},
		},
		nil,
	)

	res, _, err := GA(prob, []Objective{
		{Name: "cost", Dir: Minimize},
		{Name: "urgency", Dir: Maximize},
	}, GAConfig{
		PopulationSize: 50,
		Generations:    40,
		Seed:           7,
	})
	if err != nil {
		t.Fatalf("GA: %v", err)
	}

	if len(res.Frontier) < 2 {
		t.Errorf("expected multi-point frontier, got %d solutions", len(res.Frontier))
	}
}

func TestConstraintDominates_FeasibleBeatsInfeasible(t *testing.T) {
	feasible := scoredIndividual{Violation: 0}
	infeasible := scoredIndividual{Violation: 5}

	if !constraintDominates(feasible, infeasible) {
		t.Error("feasible should dominate infeasible")
	}
	if constraintDominates(infeasible, feasible) {
		t.Error("infeasible should NOT dominate feasible")
	}
}

func TestConstraintDominates_LessInfeasibleWins(t *testing.T) {
	a := scoredIndividual{Violation: 2}
	b := scoredIndividual{Violation: 5}

	if !constraintDominates(a, b) {
		t.Error("smaller violation should dominate larger")
	}
}

func TestSubsetProblem_MaskInvariant(t *testing.T) {
	prob := NewSubsetProblem(10, 4, nil, nil)
	rng := rngWithSeed(1)

	for i := 0; i < 20; i++ {
		ind := prob.InitialPopulation(1, rng)[0]
		mask := ind.Row.([]bool)
		if count(mask) != 4 {
			t.Fatalf("initial mask has %d true bits, want 4 (iter %d)", count(mask), i)
		}
	}

	parents := prob.InitialPopulation(2, rng)
	for i := 0; i < 50; i++ {
		child := prob.Crossover(parents[0], parents[1], rng)
		mask := child.Row.([]bool)
		if count(mask) != 4 {
			t.Errorf("crossover child has %d true bits, want 4", count(mask))
		}
		mutated := prob.Mutate(child, rng)
		mmask := mutated.Row.([]bool)
		if count(mmask) != 4 {
			t.Errorf("mutated child has %d true bits, want 4", count(mmask))
		}
	}
}

func maskEqual(a, b []bool) bool {
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

func count(m []bool) int {
	c := 0
	for _, v := range m {
		if v {
			c++
		}
	}
	return c
}

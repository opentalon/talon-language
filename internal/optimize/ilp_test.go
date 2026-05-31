package optimize

import (
	"testing"
)

// TestILP_KnapsackProvedOptimal is the headline test: the same knapsack
// fixture used in TestGA_SeededKnapsack_RecoversOptimum, but solved exactly
// by branch-and-bound. ILP must return value 120 with mask {0,1,1,1,0}.
func TestILP_KnapsackProvedOptimal(t *testing.T) {
	// Items: value, weight (we maximize value subject to weight <= 13)
	values := []float64{10, 40, 30, 50, 35}
	weights := []float64{5, 4, 6, 3, 8}

	res := ILP(ILPProblem{
		ObjectiveCoef:      values,
		ObjectiveDirection: Maximize,
		Constraints: []LinearConstraint{
			{Coef: weights, Op: "<=", Rhs: 13},
		},
		K: 3,
	})

	if !res.Feasible {
		t.Fatal("expected feasible solution")
	}
	if res.Objective != 120 {
		t.Errorf("objective: want 120, got %v", res.Objective)
	}
	wantMask := []bool{false, true, true, true, false}
	if !maskEqual(res.Mask, wantMask) {
		t.Errorf("mask: want %v, got %v", wantMask, res.Mask)
	}
	if len(res.Selected) != 3 || res.Selected[0] != 1 || res.Selected[1] != 2 || res.Selected[2] != 3 {
		t.Errorf("selected indices: want [1 2 3], got %v", res.Selected)
	}
}

func TestILP_Minimization(t *testing.T) {
	// Pick 2 items minimizing total cost.
	costs := []float64{5, 8, 3, 10, 2}
	res := ILP(ILPProblem{
		ObjectiveCoef:      costs,
		ObjectiveDirection: Minimize,
		K:                  2,
	})

	if !res.Feasible {
		t.Fatal("expected feasible")
	}
	// Optimum is items 4 (cost 2) and 2 (cost 3) = 5 total.
	if res.Objective != 5 {
		t.Errorf("min cost: want 5, got %v", res.Objective)
	}
}

func TestILP_Infeasible(t *testing.T) {
	// Need to pick 2 items but weight budget is too tight: items weigh 10 and 20
	// but budget is 5.
	res := ILP(ILPProblem{
		ObjectiveCoef:      []float64{1, 1},
		ObjectiveDirection: Maximize,
		Constraints: []LinearConstraint{
			{Coef: []float64{10, 20}, Op: "<=", Rhs: 5},
		},
		K: 2,
	})
	if res.Feasible {
		t.Errorf("expected infeasible, got %v", res.Selected)
	}
}

func TestILP_MultipleConstraints(t *testing.T) {
	// Pick 2 items maximizing value; weight <= 10 AND volume <= 8.
	values := []float64{6, 5, 4, 8}
	weights := []float64{3, 4, 5, 6}
	volumes := []float64{2, 3, 4, 5}

	res := ILP(ILPProblem{
		ObjectiveCoef:      values,
		ObjectiveDirection: Maximize,
		Constraints: []LinearConstraint{
			{Coef: weights, Op: "<=", Rhs: 10},
			{Coef: volumes, Op: "<=", Rhs: 8},
		},
		K: 2,
	})

	if !res.Feasible {
		t.Fatal("expected feasible")
	}
	// Item 3 (value=8, weight=6, vol=5) + item 0 (value=6, weight=3, vol=2)
	// = value 14, weight 9 ✓ ≤ 10, vol 7 ✓ ≤ 8. Item 3 + item 1 = value 13.
	// Item 3 + item 2 = weight 11 > 10 ✗.
	if res.Objective != 14 {
		t.Errorf("objective: want 14, got %v (selected=%v)", res.Objective, res.Selected)
	}
}

func TestILP_BeatsGAOnLinearProblem(t *testing.T) {
	// On a tiny linear knapsack, ILP must find the exact optimum that the
	// GA also finds — this is a regression guard against ILP regressions.
	values := []float64{15, 10, 9, 12, 8, 17}
	weights := []float64{4, 3, 2, 5, 1, 7}

	res := ILP(ILPProblem{
		ObjectiveCoef:      values,
		ObjectiveDirection: Maximize,
		Constraints: []LinearConstraint{
			{Coef: weights, Op: "<=", Rhs: 10},
		},
		K: 3,
	})

	if !res.Feasible {
		t.Fatal("expected feasible")
	}
	// Brute force the optimum: pick 3 of 6 → 20 combinations. Optimum is
	// items {0, 1, 5} value 15+10+17=42, weight 4+3+7=14 — exceeds budget.
	// Try {0, 1, 4} = 15+10+8=33, weight 4+3+1=8 ✓
	// Try {0, 1, 2} = 15+10+9=34, weight 4+3+2=9 ✓
	// Try {0, 2, 4} = 15+9+8=32, weight 4+2+1=7 ✓
	// Try {0, 3, 4} = 15+12+8=35, weight 4+5+1=10 ✓
	// Try {3, 5, 4} = 12+17+8=37, weight 5+7+1=13 ✗
	// Try {0, 3, 2} = 15+12+9=36, weight 4+5+2=11 ✗
	// Try {1, 3, 4} = 10+12+8=30, weight 3+5+1=9 ✓
	// Try {3, 2, 4} = 12+9+8=29, weight 5+2+1=8 ✓
	// Best feasible appears to be {0, 3, 4} = 35
	if res.Objective != 35 {
		t.Errorf("objective: want 35 (items 0,3,4), got %v (items %v)", res.Objective, res.Selected)
	}
}

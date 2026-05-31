package optimize

import (
	"math"
	"sort"
)

// ILPProblem describes a single-objective 0/1 subset selection problem with
// linear aggregate constraints. The variables are binary (each candidate is
// either selected or not); the objective and every constraint are linear
// sums of per-candidate coefficients.
//
//	ObjectiveCoef[i]    = coefficient of candidate i in the objective
//	ObjectiveDirection  = Minimize or Maximize
//	Constraints[k].Coef = coefficient of candidate i in constraint k
//	Constraints[k].Op   = "<=", ">=", "<", ">", "==", "!="
//	Constraints[k].Rhs  = right-hand side scalar
//	K                   = exact subset size (set to 0 to skip the cardinality constraint)
//
// The problem is solved exactly via branch-and-bound with LP relaxation
// bounding, which is fast for the small subset sizes typical of business
// rules (tens to low hundreds of candidates).
type ILPProblem struct {
	ObjectiveCoef      []float64
	ObjectiveDirection Direction
	Constraints        []LinearConstraint
	K                  int
}

// LinearConstraint represents one linear inequality (or equality) on the
// selection mask: sum(Coef[i] * mask[i]) OP Rhs.
type LinearConstraint struct {
	Coef []float64
	Op   string
	Rhs  float64
}

// ILPResult is the exact optimum of an ILPProblem: the selected subset
// (binary mask + indices), the objective value, and whether a feasible
// solution exists at all.
type ILPResult struct {
	Mask          []bool
	Selected      []int
	Objective     float64
	Feasible      bool
	NodesExplored int // branch-and-bound diagnostic
}

// ILP solves the problem to provable optimality. Returns Feasible=false if
// no selection of exactly K items satisfies every constraint.
func ILP(p ILPProblem) ILPResult {
	n := len(p.ObjectiveCoef)
	if n == 0 {
		return ILPResult{Feasible: false}
	}

	// For minimize: best = +Inf, worse = larger; for maximize: best = -Inf, worse = smaller.
	sign := 1.0
	if p.ObjectiveDirection == Maximize {
		sign = -1.0
	}
	// We internally minimize sign * objective.

	state := &bbState{
		problem:  p,
		sign:     sign,
		bestObj:  math.Inf(1),
		bestMask: nil,
	}

	// Order candidates by per-unit objective desirability (lower sign*coef
	// first → better to fix to 1). This gives the LP relaxation a tight bound
	// quickly and improves pruning.
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return sign*p.ObjectiveCoef[order[a]] < sign*p.ObjectiveCoef[order[b]]
	})

	mask := make([]bool, n)
	fixed := make([]int8, n) // -1 = unfixed, 0 = fixed false, 1 = fixed true
	for i := range fixed {
		fixed[i] = -1
	}
	state.branch(order, fixed, mask, 0)

	if state.bestMask == nil {
		return ILPResult{Feasible: false, NodesExplored: state.nodes}
	}

	selected := make([]int, 0, n)
	for i, v := range state.bestMask {
		if v {
			selected = append(selected, i)
		}
	}
	// Convert internal "minimized sign*obj" back to the user-facing value.
	obj := state.bestObj * sign
	return ILPResult{
		Mask:          state.bestMask,
		Selected:      selected,
		Objective:     obj,
		Feasible:      true,
		NodesExplored: state.nodes,
	}
}

type bbState struct {
	problem  ILPProblem
	sign     float64
	bestObj  float64
	bestMask []bool
	nodes    int
}

// branch fixes one variable at a time in `order` traversal, pruning via the
// LP relaxation bound. `idx` is the current position in `order`.
func (s *bbState) branch(order []int, fixed []int8, mask []bool, idx int) {
	s.nodes++

	// At a leaf, evaluate.
	if idx == len(order) {
		if !s.feasible(mask) {
			return
		}
		obj := s.sign * s.evaluate(mask)
		if obj < s.bestObj {
			s.bestObj = obj
			s.bestMask = append([]bool(nil), mask...)
		}
		return
	}

	// LP-relaxation bound: with the remaining vars free, what's the best
	// minimized (sign * obj) we can achieve? Use the cardinality constraint
	// and the objective alone for the bound — constraints are checked at
	// the leaf for simplicity (B&B remains exact, the bound is just looser).
	bound := s.lowerBound(order, fixed, mask, idx)
	if bound >= s.bestObj {
		return
	}

	// Also prune if no feasible cardinality assignment is possible from here.
	remainingTrue := s.problem.K - countTrue(mask)
	remainingSlots := len(order) - idx
	if s.problem.K > 0 {
		if remainingTrue < 0 || remainingTrue > remainingSlots {
			return
		}
	}

	v := order[idx]

	// Try fixing to 1 first (consistent with the objective-ordered traversal).
	mask[v] = true
	fixed[v] = 1
	s.branch(order, fixed, mask, idx+1)

	// Try fixing to 0.
	mask[v] = false
	fixed[v] = 0
	s.branch(order, fixed, mask, idx+1)

	// Restore.
	fixed[v] = -1
	mask[v] = false
}

// lowerBound returns the best (smallest) value of sign*objective the open
// subtree could possibly achieve. Greedy LP relaxation: take the most-
// negative-coefficient items first, respecting cardinality.
func (s *bbState) lowerBound(order []int, fixed []int8, mask []bool, idx int) float64 {
	// Sum the already-fixed-true contributions.
	fixedSum := 0.0
	fixedTrue := 0
	for i, v := range mask {
		if fixed[i] == 1 && v {
			fixedSum += s.sign * s.problem.ObjectiveCoef[i]
			fixedTrue++
		}
	}

	// How many more we can add.
	need := s.problem.K - fixedTrue
	if s.problem.K == 0 {
		// Unbounded selection: add every negative-contribution item to lower the sum.
		for k := idx; k < len(order); k++ {
			c := s.sign * s.problem.ObjectiveCoef[order[k]]
			if c < 0 {
				fixedSum += c
			}
		}
		return fixedSum
	}

	if need <= 0 {
		return fixedSum
	}
	// Add up to `need` of the most-favorable remaining items.
	picks := 0
	for k := idx; k < len(order) && picks < need; k++ {
		fixedSum += s.sign * s.problem.ObjectiveCoef[order[k]]
		picks++
	}
	return fixedSum
}

func (s *bbState) feasible(mask []bool) bool {
	if s.problem.K > 0 {
		c := 0
		for _, v := range mask {
			if v {
				c++
			}
		}
		if c != s.problem.K {
			return false
		}
	}
	for _, con := range s.problem.Constraints {
		sum := 0.0
		for i, v := range mask {
			if v {
				sum += con.Coef[i]
			}
		}
		if !checkConstraint(con.Op, sum, con.Rhs) {
			return false
		}
	}
	return true
}

func (s *bbState) evaluate(mask []bool) float64 {
	sum := 0.0
	for i, v := range mask {
		if v {
			sum += s.problem.ObjectiveCoef[i]
		}
	}
	return sum
}

func checkConstraint(op string, got, want float64) bool {
	switch op {
	case "<=":
		return got <= want
	case "<":
		return got < want
	case ">=":
		return got >= want
	case ">":
		return got > want
	case "==":
		return got == want
	case "!=":
		return got != want
	}
	return false
}

func countTrue(mask []bool) int {
	c := 0
	for _, v := range mask {
		if v {
			c++
		}
	}
	return c
}

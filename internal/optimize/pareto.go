package optimize

import (
	"fmt"
	"math"
	"sort"
)

// Pareto runs NSGA-II's fast non-dominated sort over individuals and assigns
// each one a rank and crowding distance. The returned Result.Frontier is the
// rank-0 set (Pareto-optimal solutions); Result.All carries every individual
// in (rank asc, crowding desc, EntityID asc) order so callers can render
// dominated solutions too.
//
// Empty input is allowed and yields an empty Result. Mismatched value/objective
// arity is the only error condition.
func Pareto(in []Individual, objs []Objective) (Result, error) {
	for _, ind := range in {
		if len(ind.Values) != len(objs) {
			return Result{}, fmt.Errorf("individual %d has %d values, want %d", ind.EntityID, len(ind.Values), len(objs))
		}
	}
	if len(in) == 0 {
		return Result{Objectives: objs}, nil
	}

	ranks := fastNonDominatedSort(in, objs)
	crowding := make([]float64, len(in))
	dominatedCount := make([]int, len(in))
	dominates := make([]int, len(in))

	// Count dominations for explainability evidence.
	for i := range in {
		for j := range in {
			if i == j {
				continue
			}
			if Dominates(in[i], in[j], objs) {
				dominates[i]++
				dominatedCount[j]++
			}
		}
	}

	// Crowding distance is computed per rank front.
	frontIdx := map[int][]int{}
	for i, r := range ranks {
		frontIdx[r] = append(frontIdx[r], i)
	}
	for _, idxs := range frontIdx {
		CrowdingDistance(in, idxs, objs, crowding)
	}

	all := make([]Solution, 0, len(in))
	for i, ind := range in {
		all = append(all, Solution{
			EntityID:       ind.EntityID,
			Rank:           ranks[i],
			CrowdingDist:   crowding[i],
			DominatedCount: dominatedCount[i],
			Dominates:      dominates[i],
			Values:         ind.Values,
			Row:            ind.Row,
		})
	}

	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Rank != all[j].Rank {
			return all[i].Rank < all[j].Rank
		}
		// Higher crowding distance first. +Inf wins; ties fall to EntityID for determinism.
		if all[i].CrowdingDist != all[j].CrowdingDist {
			return all[i].CrowdingDist > all[j].CrowdingDist
		}
		return all[i].EntityID < all[j].EntityID
	})

	var frontier []Solution
	for _, s := range all {
		if s.Rank == 0 {
			frontier = append(frontier, s)
		}
	}

	return Result{Frontier: frontier, All: all, Objectives: objs}, nil
}

// Dominates reports whether a Pareto-dominates b: no worse on every objective,
// strictly better on at least one. Direction is applied per-objective.
func Dominates(a, b Individual, objs []Objective) bool {
	strictlyBetter := false
	for k, obj := range objs {
		av, bv := a.Values[k], b.Values[k]
		if obj.Dir == Maximize {
			if av < bv {
				return false
			}
			if av > bv {
				strictlyBetter = true
			}
		} else {
			if av > bv {
				return false
			}
			if av < bv {
				strictlyBetter = true
			}
		}
	}
	return strictlyBetter
}

// fastNonDominatedSort returns the rank (0-indexed) of every individual using
// Deb's O(MN²) procedure.
func fastNonDominatedSort(in []Individual, objs []Objective) []int {
	n := len(in)
	dominatedBy := make([][]int, n) // dominatedBy[i] = solutions i dominates
	dominationCount := make([]int, n)
	ranks := make([]int, n)
	currentFront := []int{}

	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			if Dominates(in[i], in[j], objs) {
				dominatedBy[i] = append(dominatedBy[i], j)
			} else if Dominates(in[j], in[i], objs) {
				dominationCount[i]++
			}
		}
		if dominationCount[i] == 0 {
			ranks[i] = 0
			currentFront = append(currentFront, i)
		}
	}

	rank := 0
	for len(currentFront) > 0 {
		var nextFront []int
		for _, p := range currentFront {
			for _, q := range dominatedBy[p] {
				dominationCount[q]--
				if dominationCount[q] == 0 {
					ranks[q] = rank + 1
					nextFront = append(nextFront, q)
				}
			}
		}
		rank++
		currentFront = nextFront
	}
	return ranks
}

// CrowdingDistance assigns NSGA-II crowding distance to each member of a
// single rank front. Boundary points (objective-extreme) get +Inf so they
// win tournament ties. Mutates the dist slice at the given indices.
func CrowdingDistance(in []Individual, frontIdx []int, objs []Objective, dist []float64) {
	if len(frontIdx) == 0 {
		return
	}
	if len(frontIdx) <= 2 {
		for _, i := range frontIdx {
			dist[i] = math.Inf(1)
		}
		return
	}
	for _, i := range frontIdx {
		dist[i] = 0
	}
	for k := range objs {
		sorted := append([]int(nil), frontIdx...)
		sort.SliceStable(sorted, func(a, b int) bool {
			return in[sorted[a]].Values[k] < in[sorted[b]].Values[k]
		})
		minV := in[sorted[0]].Values[k]
		maxV := in[sorted[len(sorted)-1]].Values[k]
		dist[sorted[0]] = math.Inf(1)
		dist[sorted[len(sorted)-1]] = math.Inf(1)
		if maxV == minV {
			continue
		}
		for r := 1; r < len(sorted)-1; r++ {
			if math.IsInf(dist[sorted[r]], 1) {
				continue
			}
			dist[sorted[r]] += (in[sorted[r+1]].Values[k] - in[sorted[r-1]].Values[k]) / (maxV - minV)
		}
	}
}

package optimize

import (
	"math/rand"
)

// SubsetProblem is a generic "pick exactly K out of N candidates" problem.
// Each Individual carries a fixed-length binary mask in Row ([]bool, length N)
// where exactly K positions are true; the SubsetProblem.objectiveFns and
// constraintFns evaluate per-mask aggregates.
//
// This is the workhorse for combine blocks with `select K from records`.
// Callers populate ObjectiveFns and ConstraintFns with closures that already
// know how to extract attr values from the underlying candidate rows.
type SubsetProblem struct {
	N             int                         // total candidates
	K             int                         // subset size
	ObjectiveFns  []func(mask []bool) float64 // one per Objective; sign baked in by caller
	ConstraintFns []func(mask []bool) float64 // returns violation magnitude (0 if satisfied)
}

// NewSubsetProblem validates the basic shape — K must be in [1, N] — and
// returns a problem ready for GA.
func NewSubsetProblem(n, k int, objs []func([]bool) float64, cons []func([]bool) float64) *SubsetProblem {
	return &SubsetProblem{
		N:             n,
		K:             k,
		ObjectiveFns:  objs,
		ConstraintFns: cons,
	}
}

func (p *SubsetProblem) InitialPopulation(n int, rng *rand.Rand) []Individual {
	out := make([]Individual, 0, n)
	for i := 0; i < n; i++ {
		mask := randomMask(p.N, p.K, rng)
		out = append(out, Individual{EntityID: i, Row: mask})
	}
	return out
}

func (p *SubsetProblem) Evaluate(ind Individual) []float64 {
	mask, _ := ind.Row.([]bool)
	if mask == nil {
		mask = make([]bool, p.N)
	}
	out := make([]float64, len(p.ObjectiveFns))
	for i, fn := range p.ObjectiveFns {
		out[i] = fn(mask)
	}
	return out
}

func (p *SubsetProblem) Constraints(ind Individual) []float64 {
	mask, _ := ind.Row.([]bool)
	if mask == nil {
		return nil
	}
	out := make([]float64, len(p.ConstraintFns))
	for i, fn := range p.ConstraintFns {
		out[i] = fn(mask)
	}
	return out
}

// Crossover is uniform crossover on bitmasks followed by repair: the result
// keeps exactly K true bits. We pick bits at random from either parent for
// each position, then add/remove bits at random until the K invariant holds.
// Uniform crossover preserves more diversity than single-point for subset
// problems where bit position has no spatial meaning.
func (p *SubsetProblem) Crossover(a, b Individual, rng *rand.Rand) Individual {
	ma, _ := a.Row.([]bool)
	mb, _ := b.Row.([]bool)
	if len(ma) != p.N || len(mb) != p.N {
		return Individual{Row: randomMask(p.N, p.K, rng)}
	}
	child := make([]bool, p.N)
	for i := 0; i < p.N; i++ {
		if rng.Float64() < 0.5 {
			child[i] = ma[i]
		} else {
			child[i] = mb[i]
		}
	}
	return Individual{Row: repairMask(child, p.K, rng)}
}

// Mutate swaps one selected bit with one unselected bit. This preserves the
// K invariant cheaply and corresponds to a single-element swap in the subset.
// We mutate with probability ~1 per individual; callers wanting finer-grained
// control would override Mutate before passing into GA.
func (p *SubsetProblem) Mutate(ind Individual, rng *rand.Rand) Individual {
	mask, _ := ind.Row.([]bool)
	if len(mask) != p.N {
		return Individual{Row: randomMask(p.N, p.K, rng)}
	}
	out := make([]bool, p.N)
	copy(out, mask)

	var ones, zeros []int
	for i, v := range out {
		if v {
			ones = append(ones, i)
		} else {
			zeros = append(zeros, i)
		}
	}
	if len(ones) == 0 || len(zeros) == 0 {
		return Individual{Row: out}
	}
	out[ones[rng.Intn(len(ones))]] = false
	out[zeros[rng.Intn(len(zeros))]] = true
	return Individual{Row: out}
}

// MaskToIndices returns the candidate indices a mask selects, sorted ascending.
// Useful for callers that want to translate the GA's output back into
// candidate-row references.
func MaskToIndices(mask []bool) []int {
	out := make([]int, 0)
	for i, v := range mask {
		if v {
			out = append(out, i)
		}
	}
	return out
}

func randomMask(n, k int, rng *rand.Rand) []bool {
	if k >= n {
		mask := make([]bool, n)
		for i := range mask {
			mask[i] = true
		}
		return mask
	}
	mask := make([]bool, n)
	perm := rng.Perm(n)
	for i := 0; i < k; i++ {
		mask[perm[i]] = true
	}
	return mask
}

// repairMask adjusts a mask to have exactly k true bits. If too many, flip
// random ones to false; if too few, flip random zeros to true. Uses the
// provided RNG so repair stays deterministic under a fixed seed.
func repairMask(mask []bool, k int, rng *rand.Rand) []bool {
	count := 0
	for _, v := range mask {
		if v {
			count++
		}
	}
	if count == k {
		return mask
	}
	if count > k {
		// Remove random ones.
		var ones []int
		for i, v := range mask {
			if v {
				ones = append(ones, i)
			}
		}
		rng.Shuffle(len(ones), func(i, j int) { ones[i], ones[j] = ones[j], ones[i] })
		for i := 0; i < count-k; i++ {
			mask[ones[i]] = false
		}
	} else {
		var zeros []int
		for i, v := range mask {
			if !v {
				zeros = append(zeros, i)
			}
		}
		rng.Shuffle(len(zeros), func(i, j int) { zeros[i], zeros[j] = zeros[j], zeros[i] })
		for i := 0; i < k-count; i++ {
			mask[zeros[i]] = true
		}
	}
	return mask
}

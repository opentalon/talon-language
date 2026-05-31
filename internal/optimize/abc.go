package optimize

import (
	"math"
	"math/rand"
)

// ABCConfig parameterizes Karaboga's Artificial Bee Colony loop. Zero-valued
// fields fall back to the documented defaults from the original 2005 paper.
//
// ABC is well-suited to continuous bounded optimization with a noisy or
// non-differentiable objective — the "tune a ML primitive's threshold against
// labeled data" use case in Talon. Compared to GA, ABC has fewer control
// parameters (no crossover/mutation rates) and stronger automatic exploration
// via scout bees that restart abandoned food sources from random positions.
type ABCConfig struct {
	ColonySize int   // total bees = employed + onlookers; default 20
	Iterations int   // default 50
	Limit      int   // trials before a source is abandoned; default = N (number of food sources)
	Seed       int64 // 0 = use stable default (still reproducible)
}

func (c ABCConfig) withDefaults() ABCConfig {
	if c.ColonySize <= 0 {
		c.ColonySize = 20
	}
	if c.Iterations <= 0 {
		c.Iterations = 50
	}
	return c
}

// ABCBounds describes the search box for one dimension. ABC samples and
// mutates within [Min, Max]; the user can pin Min == Max to fix a dim.
type ABCBounds struct {
	Min float64
	Max float64
}

// ABCResult is the output of one ABC run: the best parameter vector found,
// its fitness, and a per-iteration history of the best fitness seen so far.
type ABCResult struct {
	Best    []float64
	Fitness float64
	History []float64
}

// ABC runs Artificial Bee Colony optimization. `fitness` should return a value
// the caller wants to *maximize* — wrap minimization problems as negative.
// `bounds` defines the search box (one entry per parameter dimension).
//
// Algorithm (Karaboga 2005):
//  1. Initialize N = ColonySize/2 food sources at random positions in the box.
//  2. Per iteration:
//     a. EMPLOYED phase: each employed bee perturbs one dim of its food source
//     using v_ij = x_ij + φ(x_ij - x_kj) where k is a random different source.
//     Replace x_i if the new position has higher fitness; else increment a
//     trial counter.
//     b. ONLOOKER phase: each onlooker picks a food source with probability
//     proportional to fitness, then perturbs that source. Same replacement rule.
//     c. SCOUT phase: any food source with trials >= Limit is abandoned —
//     the bee scouts to a fresh random position.
//  3. Return the best source ever seen.
func ABC(fitness func(x []float64) float64, bounds []ABCBounds, cfg ABCConfig) ABCResult {
	cfg = cfg.withDefaults()
	d := len(bounds)
	if d == 0 {
		return ABCResult{}
	}

	src := rand.NewSource(cfg.Seed)
	if cfg.Seed == 0 {
		src = rand.NewSource(1)
	}
	rng := rand.New(src)

	n := cfg.ColonySize / 2 // food sources
	if n < 2 {
		n = 2
	}
	limit := cfg.Limit
	if limit <= 0 {
		limit = n * d
	}

	// Initialize food sources.
	sources := make([][]float64, n)
	fit := make([]float64, n)
	trials := make([]int, n)
	for i := 0; i < n; i++ {
		sources[i] = randomPoint(bounds, rng)
		fit[i] = fitness(sources[i])
	}

	// Track best.
	bestIdx := 0
	for i := 1; i < n; i++ {
		if fit[i] > fit[bestIdx] {
			bestIdx = i
		}
	}
	best := append([]float64(nil), sources[bestIdx]...)
	bestFit := fit[bestIdx]

	history := make([]float64, 0, cfg.Iterations)

	for it := 0; it < cfg.Iterations; it++ {
		// ─── Employed phase ─────────────────────────────────────────────────
		for i := 0; i < n; i++ {
			cand := perturb(sources[i], sources, i, bounds, rng)
			cFit := fitness(cand)
			if cFit > fit[i] {
				sources[i] = cand
				fit[i] = cFit
				trials[i] = 0
			} else {
				trials[i]++
			}
		}

		// ─── Onlooker phase ─────────────────────────────────────────────────
		// Probability proportional to fitness; shift to non-negative first.
		probs := computeOnlookerProbs(fit)
		for o := 0; o < n; o++ {
			i := selectByRoulette(probs, rng)
			cand := perturb(sources[i], sources, i, bounds, rng)
			cFit := fitness(cand)
			if cFit > fit[i] {
				sources[i] = cand
				fit[i] = cFit
				trials[i] = 0
			} else {
				trials[i]++
			}
		}

		// ─── Memorize best & scout abandoned sources ────────────────────────
		for i := 0; i < n; i++ {
			if fit[i] > bestFit {
				bestFit = fit[i]
				best = append(best[:0], sources[i]...)
			}
			if trials[i] >= limit {
				sources[i] = randomPoint(bounds, rng)
				fit[i] = fitness(sources[i])
				trials[i] = 0
				if fit[i] > bestFit {
					bestFit = fit[i]
					best = append(best[:0], sources[i]...)
				}
			}
		}

		history = append(history, bestFit)
	}

	return ABCResult{Best: append([]float64(nil), best...), Fitness: bestFit, History: history}
}

func randomPoint(bounds []ABCBounds, rng *rand.Rand) []float64 {
	p := make([]float64, len(bounds))
	for i, b := range bounds {
		if b.Min == b.Max {
			p[i] = b.Min
			continue
		}
		p[i] = b.Min + rng.Float64()*(b.Max-b.Min)
	}
	return p
}

// perturb produces a candidate by mutating one random dimension of source[i]
// using the ABC update rule v_ij = x_ij + φ(x_ij - x_kj), φ ∈ [-1, 1],
// where k is a different randomly-chosen source.
func perturb(source []float64, all [][]float64, i int, bounds []ABCBounds, rng *rand.Rand) []float64 {
	out := make([]float64, len(source))
	copy(out, source)

	d := len(source)
	if d == 0 {
		return out
	}
	j := rng.Intn(d)

	// Pick a partner k != i.
	k := rng.Intn(len(all))
	if len(all) > 1 && k == i {
		k = (k + 1) % len(all)
	}

	phi := -1 + 2*rng.Float64()
	out[j] = source[j] + phi*(source[j]-all[k][j])

	// Clamp to bounds.
	if out[j] < bounds[j].Min {
		out[j] = bounds[j].Min
	}
	if out[j] > bounds[j].Max {
		out[j] = bounds[j].Max
	}
	return out
}

// computeOnlookerProbs shifts the fitness vector to non-negative and
// normalizes. ABC's original formulation uses 1/(1+f) for minimization or f
// directly for maximization; since we already wrap minimization as negation,
// we just rebase to non-negative and normalize.
func computeOnlookerProbs(fit []float64) []float64 {
	minF := math.Inf(1)
	for _, f := range fit {
		if f < minF {
			minF = f
		}
	}
	shifted := make([]float64, len(fit))
	var total float64
	for i, f := range fit {
		shifted[i] = f - minF + 1e-9 // ensure strictly positive
		total += shifted[i]
	}
	if total == 0 {
		// Degenerate: uniform.
		p := 1.0 / float64(len(fit))
		for i := range shifted {
			shifted[i] = p
		}
		return shifted
	}
	for i := range shifted {
		shifted[i] /= total
	}
	return shifted
}

func selectByRoulette(probs []float64, rng *rand.Rand) int {
	r := rng.Float64()
	var cum float64
	for i, p := range probs {
		cum += p
		if r < cum {
			return i
		}
	}
	return len(probs) - 1
}

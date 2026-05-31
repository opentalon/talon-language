package optimize

import (
	"fmt"
	"math/rand"
	"sort"
)

// Problem is the GA's view of a problem. Each Individual is opaque to the GA
// (typically a binary mask, permutation, or parameter vector); the problem
// knows how to seed an initial population, score it against the objectives,
// measure constraint violations, and recombine genes via Crossover/Mutate.
//
// Constraints return one float64 per constraint: 0 means satisfied; a positive
// number is the magnitude of violation. The GA aggregates violations into a
// single "feasibility score" for Deb's constraint-dominance rule, which is
// what makes constrained Pareto search work without ad-hoc penalty weights.
type Problem interface {
	InitialPopulation(n int, rng *rand.Rand) []Individual
	Evaluate(ind Individual) []float64
	Constraints(ind Individual) []float64
	Crossover(a, b Individual, rng *rand.Rand) Individual
	Mutate(ind Individual, rng *rand.Rand) Individual
}

// GAConfig parameterizes the genetic algorithm loop. Zero-valued fields fall
// back to documented defaults so callers can pass GAConfig{} and get sensible
// behavior; Seed == 0 means non-deterministic (wall clock).
type GAConfig struct {
	PopulationSize int     // default 100
	Generations    int     // default 100
	CrossoverRate  float64 // default 0.9
	MutationRate   float64 // default 0.05 (used by SubsetProblem.Mutate; problem-specific operators may ignore)
	TournamentK    int     // default 2
	Seed           int64   // 0 = non-deterministic
}

func (c GAConfig) withDefaults() GAConfig {
	if c.PopulationSize <= 0 {
		c.PopulationSize = 100
	}
	if c.Generations <= 0 {
		c.Generations = 100
	}
	if c.CrossoverRate <= 0 {
		c.CrossoverRate = 0.9
	}
	if c.MutationRate <= 0 {
		c.MutationRate = 0.05
	}
	if c.TournamentK <= 0 {
		c.TournamentK = 2
	}
	return c
}

// GenerationStats summarizes one generation for the audit trail. The GA emits
// one per generation so `talon explain` can render "ran 100 generations,
// hypervolume plateaued at gen 47" without storing the entire population.
type GenerationStats struct {
	Generation     int
	FrontierSize   int
	BestObjectives []float64 // best (per objective) seen so far
	FeasibleRatio  float64   // fraction of population that is feasible
}

// scoredIndividual is the GA's internal bookkeeping for one individual:
// its objective values, constraint violation (summed), and population indices.
type scoredIndividual struct {
	Ind       Individual
	Values    []float64
	Violation float64 // 0 = feasible; > 0 = magnitude of violation
}

// GA runs the genetic algorithm and returns the final population's Pareto
// front plus a per-generation stats trace.
//
// The loop is textbook NSGA-II with Deb's constraint-dominance:
//
//  1. Initialize a random population via Problem.InitialPopulation.
//  2. For each generation:
//     a. Produce offspring via tournament selection → crossover → mutation.
//     b. Combine parent + offspring (μ + λ).
//     c. Sort by constraint-dominance rank, then crowding distance.
//     d. Take the top PopulationSize survivors.
//  3. Return rank-0 of the final population as Frontier.
//
// Determinism: with Seed != 0, two runs against the same Problem produce
// identical Frontier (modulo Go map iteration order, which the GA does not
// rely on internally).
func GA(prob Problem, objs []Objective, cfg GAConfig) (Result, []GenerationStats, error) {
	cfg = cfg.withDefaults()
	if prob == nil {
		return Result{}, nil, fmt.Errorf("optimize.GA: nil Problem")
	}

	src := rand.NewSource(cfg.Seed)
	if cfg.Seed == 0 {
		src = rand.NewSource(1) // documented-stable fallback; callers wanting wall-clock pass their own
	}
	rng := rand.New(src)

	pop := scorePopulation(prob, objs, prob.InitialPopulation(cfg.PopulationSize, rng))
	if len(pop) == 0 {
		return Result{Objectives: objs}, nil, nil
	}

	var stats []GenerationStats
	for gen := 0; gen < cfg.Generations; gen++ {
		offspring := makeOffspring(prob, pop, cfg, rng)
		merged := append([]scoredIndividual{}, pop...)
		merged = append(merged, scorePopulation(prob, objs, offspring)...)

		ranks := constraintDominanceSort(merged, objs)
		crowding := make([]float64, len(merged))
		frontIdx := map[int][]int{}
		for i, r := range ranks {
			frontIdx[r] = append(frontIdx[r], i)
		}
		toInds := make([]Individual, len(merged))
		for i, m := range merged {
			toInds[i] = Individual{EntityID: i, Values: m.Values}
		}
		for _, idxs := range frontIdx {
			CrowdingDistance(toInds, idxs, objs, crowding)
		}

		// Survivor selection: rank asc, then crowding desc.
		order := make([]int, len(merged))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool {
			if ranks[order[a]] != ranks[order[b]] {
				return ranks[order[a]] < ranks[order[b]]
			}
			return crowding[order[a]] > crowding[order[b]]
		})

		next := make([]scoredIndividual, 0, cfg.PopulationSize)
		for i := 0; i < cfg.PopulationSize && i < len(order); i++ {
			next = append(next, merged[order[i]])
		}
		pop = next

		stats = append(stats, snapshotStats(pop, objs, gen))
	}

	return finalize(pop, objs), stats, nil
}

func scorePopulation(prob Problem, objs []Objective, inds []Individual) []scoredIndividual {
	out := make([]scoredIndividual, 0, len(inds))
	for _, ind := range inds {
		values := prob.Evaluate(ind)
		if len(values) != len(objs) {
			// Skip malformed individuals rather than panicking — initial
			// populations from a buggy Problem shouldn't crash the GA.
			continue
		}
		viols := prob.Constraints(ind)
		var totalViol float64
		for _, v := range viols {
			if v > 0 {
				totalViol += v
			}
		}
		out = append(out, scoredIndividual{Ind: ind, Values: values, Violation: totalViol})
	}
	return out
}

func makeOffspring(prob Problem, pop []scoredIndividual, cfg GAConfig, rng *rand.Rand) []Individual {
	out := make([]Individual, 0, cfg.PopulationSize)
	for len(out) < cfg.PopulationSize {
		a := tournament(pop, cfg.TournamentK, rng)
		b := tournament(pop, cfg.TournamentK, rng)
		child := a.Ind
		if rng.Float64() < cfg.CrossoverRate {
			child = prob.Crossover(a.Ind, b.Ind, rng)
		}
		child = prob.Mutate(child, rng)
		out = append(out, child)
	}
	return out
}

// tournament picks K random individuals and returns the constraint-dominant
// winner. With K=2 this is the classic binary tournament; larger K applies
// more selection pressure.
func tournament(pop []scoredIndividual, k int, rng *rand.Rand) scoredIndividual {
	best := pop[rng.Intn(len(pop))]
	for i := 1; i < k; i++ {
		c := pop[rng.Intn(len(pop))]
		if constraintDominates(c, best) {
			best = c
		}
	}
	return best
}

// constraintDominates applies Deb's rule:
//  1. If a is feasible and b is not, a dominates b.
//  2. If both are infeasible, the one with smaller violation dominates.
//  3. If both are feasible, regular Pareto dominance applies.
//
// (We use a directional helper that needs objectives; tournament uses the
// lighter rule below since it only compares two individuals.)
func constraintDominates(a, b scoredIndividual) bool {
	if a.Violation == 0 && b.Violation > 0 {
		return true
	}
	if a.Violation > 0 && b.Violation == 0 {
		return false
	}
	if a.Violation > 0 && b.Violation > 0 {
		return a.Violation < b.Violation
	}
	// Both feasible — tournament can't rank without the objective vector,
	// fall back to "neither dominates" (caller picks randomly via the loop).
	return false
}

// constraintDominanceSort assigns a constraint-Pareto rank to every member of
// pop. Among feasible solutions, regular fast-non-dominated-sort applies.
// Infeasible solutions are sorted into later ranks by violation magnitude.
func constraintDominanceSort(pop []scoredIndividual, objs []Objective) []int {
	n := len(pop)
	ranks := make([]int, n)

	// Bucket: feasible (violation == 0) get sorted via standard Pareto.
	// Infeasible get appended after with rank = lastFeasibleRank + sortedViolationIndex.
	var feasIdx, infeasIdx []int
	for i, p := range pop {
		if p.Violation == 0 {
			feasIdx = append(feasIdx, i)
		} else {
			infeasIdx = append(infeasIdx, i)
		}
	}

	maxFeasRank := -1
	if len(feasIdx) > 0 {
		feasInd := make([]Individual, len(feasIdx))
		for k, i := range feasIdx {
			feasInd[k] = Individual{EntityID: i, Values: pop[i].Values}
		}
		feasRanks := fastNonDominatedSort(feasInd, objs)
		for k, r := range feasRanks {
			ranks[feasIdx[k]] = r
			if r > maxFeasRank {
				maxFeasRank = r
			}
		}
	}

	if len(infeasIdx) > 0 {
		// Sort infeasible ascending by violation; rank them after feasible.
		sort.SliceStable(infeasIdx, func(i, j int) bool {
			return pop[infeasIdx[i]].Violation < pop[infeasIdx[j]].Violation
		})
		for k, i := range infeasIdx {
			ranks[i] = maxFeasRank + 1 + k
		}
	}

	return ranks
}

func snapshotStats(pop []scoredIndividual, objs []Objective, gen int) GenerationStats {
	if len(pop) == 0 {
		return GenerationStats{Generation: gen}
	}
	best := make([]float64, len(objs))
	for i, obj := range objs {
		v := pop[0].Values[i]
		for _, p := range pop {
			if obj.Dir == Maximize {
				if p.Values[i] > v {
					v = p.Values[i]
				}
			} else {
				if p.Values[i] < v {
					v = p.Values[i]
				}
			}
		}
		best[i] = v
	}
	feasible := 0
	for _, p := range pop {
		if p.Violation == 0 {
			feasible++
		}
	}
	// Frontier size in this snapshot.
	feasOnly := make([]Individual, 0, len(pop))
	for _, p := range pop {
		if p.Violation == 0 {
			feasOnly = append(feasOnly, Individual{Values: p.Values})
		}
	}
	frontierSize := 0
	if len(feasOnly) > 0 {
		rs := fastNonDominatedSort(feasOnly, objs)
		for _, r := range rs {
			if r == 0 {
				frontierSize++
			}
		}
	}
	return GenerationStats{
		Generation:     gen,
		FrontierSize:   frontierSize,
		BestObjectives: best,
		FeasibleRatio:  float64(feasible) / float64(len(pop)),
	}
}

// finalize maps the final population's rank-0 feasible members to a
// user-facing Result. Infeasible solutions never appear in the Frontier
// even if the GA could not find a feasible point — callers detect this via
// an empty Frontier and a fully infeasible All list.
func finalize(pop []scoredIndividual, objs []Objective) Result {
	// Only feasible individuals are eligible for the Pareto frontier. The GA
	// keeps infeasible solutions around for diversity (Deb's rule lets them
	// participate in selection) but they must never be returned as "optimal."
	feasIndividuals := make([]Individual, 0, len(pop))
	feasRowByEntID := map[int]any{}
	for i, p := range pop {
		if p.Violation > 0 {
			continue
		}
		feasIndividuals = append(feasIndividuals, Individual{EntityID: i, Values: p.Values, Row: p.Ind.Row})
		feasRowByEntID[i] = p.Ind.Row
	}

	if len(feasIndividuals) == 0 {
		// No feasible solution found. Return an All list reflecting all
		// individuals (with rank 0, since we can't compute a meaningful rank
		// over infeasibles) so callers can still inspect the population.
		all := make([]Solution, len(pop))
		for i, p := range pop {
			all[i] = Solution{EntityID: i, Values: p.Values, Row: p.Ind.Row, Rank: 0}
		}
		return Result{All: all, Objectives: objs}
	}

	res, _ := Pareto(feasIndividuals, objs)
	for i, s := range res.All {
		res.All[i].Row = feasRowByEntID[s.EntityID]
	}
	for i, s := range res.Frontier {
		res.Frontier[i].Row = feasRowByEntID[s.EntityID]
	}
	return res
}

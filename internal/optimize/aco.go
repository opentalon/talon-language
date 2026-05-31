package optimize

import (
	"math"
	"math/rand"
)

// ACOConfig tunes the Ant Colony Optimization loop. Zero-valued fields fall
// back to documented defaults so callers can pass ACOConfig{} and get the
// standard parameters from Dorigo's Ant System paper.
type ACOConfig struct {
	Ants         int     // default 20
	Iterations   int     // default 100
	Alpha        float64 // pheromone influence; default 1.0
	Beta         float64 // heuristic (1/distance) influence; default 3.0
	Evaporation  float64 // ρ in (1-ρ); default 0.1
	Deposit      float64 // Q in Q/length; default 100
	InitialTrail float64 // initial pheromone on every edge; default 1.0
	Seed         int64   // 0 = non-deterministic
}

func (c ACOConfig) withDefaults() ACOConfig {
	if c.Ants <= 0 {
		c.Ants = 20
	}
	if c.Iterations <= 0 {
		c.Iterations = 100
	}
	if c.Alpha <= 0 {
		c.Alpha = 1.0
	}
	if c.Beta <= 0 {
		c.Beta = 3.0
	}
	if c.Evaporation <= 0 {
		c.Evaporation = 0.1
	}
	if c.Deposit <= 0 {
		c.Deposit = 100
	}
	if c.InitialTrail <= 0 {
		c.InitialTrail = 1.0
	}
	return c
}

// ACOResult is the output of one ACO run: the best tour found (a permutation
// of node indices), its total length, and a per-iteration trace of best
// lengths so `talon explain` can render convergence.
type ACOResult struct {
	Tour    []int
	Length  float64
	History []float64
}

// ACO runs Ant Colony Optimization over a fully-connected graph whose edge
// weights are the symmetric `dist` matrix (dist[i][j] = distance from i to j;
// dist[i][i] should be 0). The result is a Hamiltonian cycle visiting every
// node, with the minimum total distance the ants converged on.
//
// The implementation is classic Ant System: each iteration, every ant builds
// a tour by probabilistic node selection weighted by (τ^α · η^β), pheromone
// evaporates globally by (1-ρ), and each ant deposits Q/length on its tour's
// edges. The best-so-far tour is also reinforced ("elitist Ant System").
func ACO(dist [][]float64, cfg ACOConfig) ACOResult {
	cfg = cfg.withDefaults()
	n := len(dist)
	if n == 0 {
		return ACOResult{}
	}
	if n == 1 {
		return ACOResult{Tour: []int{0}, Length: 0}
	}

	src := rand.NewSource(cfg.Seed)
	if cfg.Seed == 0 {
		src = rand.NewSource(1)
	}
	rng := rand.New(src)

	// Pheromone matrix (symmetric).
	pher := make([][]float64, n)
	for i := range pher {
		pher[i] = make([]float64, n)
		for j := range pher[i] {
			pher[i][j] = cfg.InitialTrail
		}
	}

	// Precompute the heuristic 1/distance once; small ε avoids division by 0
	// on the diagonal (which is never read but keeps the math safe).
	heur := make([][]float64, n)
	for i := range heur {
		heur[i] = make([]float64, n)
		for j := range heur[i] {
			if i == j {
				continue
			}
			d := dist[i][j]
			if d == 0 {
				heur[i][j] = 1e9 // coincident points — strong attractor
			} else {
				heur[i][j] = 1.0 / d
			}
		}
	}

	bestTour := []int{}
	bestLen := math.Inf(1)
	history := make([]float64, 0, cfg.Iterations)

	for it := 0; it < cfg.Iterations; it++ {
		tours := make([][]int, cfg.Ants)
		lengths := make([]float64, cfg.Ants)
		for a := 0; a < cfg.Ants; a++ {
			tour := antTour(n, pher, heur, cfg.Alpha, cfg.Beta, rng)
			tours[a] = tour
			lengths[a] = tourLength(tour, dist)
			if lengths[a] < bestLen {
				bestLen = lengths[a]
				bestTour = append([]int(nil), tour...)
			}
		}

		// Evaporate.
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				pher[i][j] *= (1 - cfg.Evaporation)
			}
		}

		// Deposit from each ant's tour.
		for a, tour := range tours {
			depo := cfg.Deposit / lengths[a]
			for k := 0; k < len(tour); k++ {
				i := tour[k]
				j := tour[(k+1)%len(tour)]
				pher[i][j] += depo
				pher[j][i] += depo
			}
		}

		// Elitist reinforcement of the best-so-far tour.
		if len(bestTour) > 0 {
			depo := cfg.Deposit / bestLen
			for k := 0; k < len(bestTour); k++ {
				i := bestTour[k]
				j := bestTour[(k+1)%len(bestTour)]
				pher[i][j] += depo
				pher[j][i] += depo
			}
		}

		history = append(history, bestLen)
	}

	return ACOResult{Tour: bestTour, Length: bestLen, History: history}
}

// antTour builds one ant's Hamiltonian cycle. Start node is random; each next
// node is chosen by roulette over (τ^α · η^β) among unvisited candidates.
func antTour(n int, pher, heur [][]float64, alpha, beta float64, rng *rand.Rand) []int {
	visited := make([]bool, n)
	tour := make([]int, 0, n)
	start := rng.Intn(n)
	tour = append(tour, start)
	visited[start] = true

	for step := 1; step < n; step++ {
		current := tour[len(tour)-1]
		probs := make([]float64, n)
		var total float64
		for j := 0; j < n; j++ {
			if visited[j] {
				continue
			}
			probs[j] = math.Pow(pher[current][j], alpha) * math.Pow(heur[current][j], beta)
			total += probs[j]
		}

		next := -1
		if total <= 0 {
			// Fallback: pick the first unvisited (shouldn't happen with normal pheromone levels).
			for j := 0; j < n; j++ {
				if !visited[j] {
					next = j
					break
				}
			}
		} else {
			r := rng.Float64() * total
			var cum float64
			for j := 0; j < n; j++ {
				if visited[j] {
					continue
				}
				cum += probs[j]
				if cum >= r {
					next = j
					break
				}
			}
			if next == -1 {
				for j := 0; j < n; j++ {
					if !visited[j] {
						next = j
					}
				}
			}
		}

		tour = append(tour, next)
		visited[next] = true
	}
	return tour
}

func tourLength(tour []int, dist [][]float64) float64 {
	total := 0.0
	n := len(tour)
	for i := 0; i < n; i++ {
		total += dist[tour[i]][tour[(i+1)%n]]
	}
	return total
}

// EuclideanDistanceMatrix builds a symmetric pairwise euclidean distance
// matrix from N two-dimensional points. Convenience helper for callers
// (combine sequence mode uses it on the rows' coordinate attrs).
func EuclideanDistanceMatrix(xs, ys []float64) [][]float64 {
	n := len(xs)
	out := make([][]float64, n)
	for i := 0; i < n; i++ {
		out[i] = make([]float64, n)
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			dx := xs[i] - xs[j]
			dy := ys[i] - ys[j]
			out[i][j] = math.Sqrt(dx*dx + dy*dy)
		}
	}
	return out
}

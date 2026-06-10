package mlruntime

// MarkovChain is an empirical first-order Markov chain built from a
// sequence of observed states. Each pair of adjacent observations
// in the input becomes one transition; the chain's row vectors are
// normalised so each row sums to 1 (probability of moving FROM
// state X TO state Y in one step).
//
// First-order: P(next | current) only — no longer history.
// Higher-order chains (P(next | last K) for K > 1) are a separate
// follow-up since they need a different state-space representation.
//
// Used by:
//   - State-progression forecasting ("how likely is this order to
//     reach SHIPPED in 3 steps from APPROVED?")
//   - Per-entity drift detection (transition matrix today vs
//     reference baseline)
type MarkovChain struct {
	States []string
	// Transition[i][j] = P(next = States[j] | current = States[i])
	Transition [][]float64
}

// FitMarkov returns a Markov chain estimated from `observations`. The
// state alphabet is inferred from the data — every distinct value
// becomes a state. Order in States is stable (first-seen) so the
// matrix is reproducible across runs.
//
// Sequences of length < 2 yield a chain with no transitions; that's
// not an error — it's just an under-determined chain whose Predict
// will return 0 probability for any non-self target.
func FitMarkov(observations []string) MarkovChain {
	if len(observations) < 2 {
		// Still record states so Predict has a defined alphabet.
		seen := map[string]int{}
		states := []string{}
		for _, o := range observations {
			if _, ok := seen[o]; !ok {
				seen[o] = len(states)
				states = append(states, o)
			}
		}
		return MarkovChain{States: states, Transition: makeMatrix(len(states))}
	}

	idx := map[string]int{}
	states := []string{}
	add := func(s string) int {
		if i, ok := idx[s]; ok {
			return i
		}
		idx[s] = len(states)
		states = append(states, s)
		return idx[s]
	}
	for _, o := range observations {
		add(o)
	}

	n := len(states)
	counts := makeMatrix(n)
	rowTotal := make([]float64, n)
	for i := 0; i < len(observations)-1; i++ {
		from := idx[observations[i]]
		to := idx[observations[i+1]]
		counts[from][to]++
		rowTotal[from]++
	}

	// Row-normalise. Rows with no observed outgoing transitions stay
	// all-zero — i.e. "absorbing in the empirical sample". Predict
	// treats those as "unknown future" — returns 0 to non-self.
	for i := 0; i < n; i++ {
		if rowTotal[i] == 0 {
			continue
		}
		for j := 0; j < n; j++ {
			counts[i][j] /= rowTotal[i]
		}
	}

	return MarkovChain{States: states, Transition: counts}
}

// Predict returns P(state == target | start = from, after exactly
// `steps` transitions). steps must be ≥ 0; 0 means "already in
// target" (returns 1 if from == target else 0).
//
// Implementation: repeated matrix-vector product over the row-
// stochastic transition matrix. O(steps × |States|²) — fine for
// the alphabet sizes (≤ ~100 states) typical of lifecycle FSMs.
//
// from / target not in States returns 0; this lets callers ask
// "what's the chance of reaching a state we've never seen?" and
// get the honest answer.
func (m MarkovChain) Predict(from, target string, steps int) float64 {
	if steps < 0 {
		return 0
	}
	if steps == 0 {
		if from == target {
			return 1
		}
		return 0
	}
	fromIdx := indexOf(m.States, from)
	targetIdx := indexOf(m.States, target)
	if fromIdx < 0 || targetIdx < 0 {
		return 0
	}
	n := len(m.States)
	// Distribution vector. Start: 1.0 mass at fromIdx.
	cur := make([]float64, n)
	cur[fromIdx] = 1
	next := make([]float64, n)
	for s := 0; s < steps; s++ {
		for i := 0; i < n; i++ {
			next[i] = 0
		}
		for i := 0; i < n; i++ {
			if cur[i] == 0 {
				continue
			}
			for j := 0; j < n; j++ {
				next[j] += cur[i] * m.Transition[i][j]
			}
		}
		cur, next = next, cur
	}
	return cur[targetIdx]
}

// SteadyState returns the long-run stationary distribution by
// iterating power-method until the L1 norm change between
// successive distributions falls below 1e-9 or maxIter is hit.
// Useful for capacity-planning style questions ("in the limit,
// what fraction of orders sit in each state?").
func (m MarkovChain) SteadyState(maxIter int) map[string]float64 {
	n := len(m.States)
	if n == 0 {
		return map[string]float64{}
	}
	cur := make([]float64, n)
	for i := range cur {
		cur[i] = 1.0 / float64(n)
	}
	next := make([]float64, n)
	for k := 0; k < maxIter; k++ {
		for i := 0; i < n; i++ {
			next[i] = 0
		}
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				next[j] += cur[i] * m.Transition[i][j]
			}
		}
		delta := 0.0
		for i := 0; i < n; i++ {
			d := next[i] - cur[i]
			if d < 0 {
				d = -d
			}
			delta += d
		}
		cur, next = next, cur
		if delta < 1e-9 {
			break
		}
	}
	out := make(map[string]float64, n)
	for i, s := range m.States {
		out[s] = cur[i]
	}
	return out
}

func makeMatrix(n int) [][]float64 {
	m := make([][]float64, n)
	for i := range m {
		m[i] = make([]float64, n)
	}
	return m
}

func indexOf(s []string, target string) int {
	for i, v := range s {
		if v == target {
			return i
		}
	}
	return -1
}

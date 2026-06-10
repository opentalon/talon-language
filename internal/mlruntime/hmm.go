package mlruntime

import "math"

// HMM is a Hidden Markov Model with discrete states and discrete
// observations. Used for anomaly detection where the "true state"
// of an entity (FAULTY vs HEALTHY) isn't directly observable but
// produces noisy observable signals.
//
// Forward-algorithm scoring: given a parametrised model and an
// observation sequence, compute P(observations | model). Low
// probability under a "healthy" model is the anomaly signal.
//
// Talon's primitive only consumes models — there's no training
// surface yet (no Baum-Welch). Callers parameterise the model
// inline (small expert-systems-style models) or from a separate
// training pipeline (host responsibility).
type HMM struct {
	States       []string    // index → state name
	Observations []string    // index → observation symbol
	Initial      []float64   // P(starting in state i)
	Trans        [][]float64 // Trans[i][j] = P(next = j | current = i)
	Emit         [][]float64 // Emit[i][o]  = P(emit obs o | state i)
}

// LogLikelihood returns log P(observations | model) computed via the
// forward algorithm with log-domain summation (log-sum-exp). Log
// domain avoids underflow on long sequences where products of
// probabilities approach zero.
//
// Returns math.Inf(-1) on an empty sequence (no information) or a
// sequence containing an unknown observation symbol — callers
// treat -Inf as "this can't have come from the model".
//
// Complexity: O(T × |States|²) for T-length observations.
func (h HMM) LogLikelihood(observations []string) float64 {
	if len(observations) == 0 {
		return math.Inf(-1)
	}
	obsIdx := indexMap(h.Observations)
	first, ok := obsIdx[observations[0]]
	if !ok {
		return math.Inf(-1)
	}
	n := len(h.States)
	// alpha[i] = log P(observations[0..t], state_t = i)
	alpha := make([]float64, n)
	for i := 0; i < n; i++ {
		alpha[i] = logSafe(h.Initial[i]) + logSafe(h.Emit[i][first])
	}
	next := make([]float64, n)
	for t := 1; t < len(observations); t++ {
		ot, ok := obsIdx[observations[t]]
		if !ok {
			return math.Inf(-1)
		}
		for j := 0; j < n; j++ {
			// log Σ_i alpha[i] * Trans[i][j] = logsumexp over i of (alpha[i] + log Trans[i][j])
			terms := make([]float64, 0, n)
			for i := 0; i < n; i++ {
				terms = append(terms, alpha[i]+logSafe(h.Trans[i][j]))
			}
			next[j] = logSumExp(terms) + logSafe(h.Emit[j][ot])
		}
		alpha, next = next, alpha
	}
	return logSumExp(alpha)
}

// AnomalyScore returns a [0, 1] anomaly intensity for a sequence:
// the higher, the more anomalous *relative to* a healthy model.
// Cutoff is the threshold log-likelihood below which we consider
// the sequence anomalous; the score linearly maps the log-
// likelihood gap to [0, 1] with floor/ceiling clamping. Caller
// picks Cutoff from baseline calibration data.
//
// Returns 1 (max anomaly) when log-likelihood is -Inf (impossible
// sequence) and 0 when log-likelihood is at or above Cutoff.
func (h HMM) AnomalyScore(observations []string, cutoff float64) float64 {
	ll := h.LogLikelihood(observations)
	if math.IsInf(ll, -1) {
		return 1
	}
	if ll >= cutoff {
		return 0
	}
	// gap is in nats (natural-log units). Saturate at 10 nats of
	// gap — that's 22000× less likely than threshold, plenty for
	// the [0, 1] scale.
	gap := cutoff - ll
	if gap > 10 {
		gap = 10
	}
	return gap / 10
}

func logSafe(p float64) float64 {
	if p <= 0 {
		return math.Inf(-1)
	}
	return math.Log(p)
}

// logSumExp returns log(Σ exp(x_i)) computed in a numerically
// stable way: factor out the max, take logs, sum, undo. Standard
// trick to avoid overflow/underflow in HMM forward-backward.
func logSumExp(xs []float64) float64 {
	if len(xs) == 0 {
		return math.Inf(-1)
	}
	max := xs[0]
	for _, x := range xs[1:] {
		if x > max {
			max = x
		}
	}
	if math.IsInf(max, -1) {
		return math.Inf(-1)
	}
	sum := 0.0
	for _, x := range xs {
		sum += math.Exp(x - max)
	}
	return max + math.Log(sum)
}

func indexMap(xs []string) map[string]int {
	m := make(map[string]int, len(xs))
	for i, x := range xs {
		m[x] = i
	}
	return m
}

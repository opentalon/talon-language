package mlruntime

import (
	"math"
	"testing"
)

func TestMarkov_FitAndPredict_OneStep(t *testing.T) {
	// Toy lifecycle. The flat sequence contains transitions back to
	// "pending" via shipped/cancelled (since FitMarkov processes
	// every adjacent pair). From "pending" we observe approved 3
	// times and cancelled 1 time — so P(pending → approved, 1) =
	// 3/4 not 2/3.
	obs := []string{
		"pending", "approved", "shipped",
		"pending", "approved", "shipped",
		"pending", "cancelled",
		"pending", "approved", "shipped",
	}
	chain := FitMarkov(obs)

	p := chain.Predict("pending", "approved", 1)
	if math.Abs(p-0.75) > 1e-9 {
		t.Errorf("P(pending → approved, 1 step) = %v, want 3/4", p)
	}
	p = chain.Predict("pending", "cancelled", 1)
	if math.Abs(p-0.25) > 1e-9 {
		t.Errorf("P(pending → cancelled, 1 step) = %v, want 1/4", p)
	}
	// approved → shipped is the only observed approved transition.
	p = chain.Predict("approved", "shipped", 1)
	if math.Abs(p-1.0) > 1e-9 {
		t.Errorf("P(approved → shipped, 1 step) = %v, want 1", p)
	}
}

func TestMarkov_Predict_TwoStep(t *testing.T) {
	// P(pending → shipped, 2 steps) = P(pending → approved) × P(approved → shipped)
	// + P(pending → cancelled) × P(cancelled → shipped)
	//                                = 3/4 × 1 + 1/4 × 0 = 3/4.
	obs := []string{
		"pending", "approved", "shipped",
		"pending", "approved", "shipped",
		"pending", "cancelled",
		"pending", "approved", "shipped",
	}
	chain := FitMarkov(obs)
	p := chain.Predict("pending", "shipped", 2)
	if math.Abs(p-0.75) > 1e-9 {
		t.Errorf("P(pending → shipped, 2 steps) = %v, want 3/4", p)
	}
}

func TestMarkov_Predict_ZeroSteps(t *testing.T) {
	chain := FitMarkov([]string{"a", "b", "a", "b"})
	if p := chain.Predict("a", "a", 0); p != 1 {
		t.Errorf("zero-step self-prob = %v, want 1", p)
	}
	if p := chain.Predict("a", "b", 0); p != 0 {
		t.Errorf("zero-step diff = %v, want 0", p)
	}
}

func TestMarkov_Predict_UnknownState(t *testing.T) {
	chain := FitMarkov([]string{"a", "b", "a", "b"})
	if p := chain.Predict("a", "ghost", 5); p != 0 {
		t.Errorf("P(reach unknown state) = %v, want 0", p)
	}
}

func TestMarkov_SteadyState_TwoStateCycle(t *testing.T) {
	// a ↔ b cycle: stationary distribution should be ~ {a: 0.5, b: 0.5}.
	chain := FitMarkov([]string{"a", "b", "a", "b", "a", "b"})
	ss := chain.SteadyState(1000)
	if math.Abs(ss["a"]-0.5) > 1e-6 {
		t.Errorf("steady-state a = %v, want ~0.5", ss["a"])
	}
	if math.Abs(ss["b"]-0.5) > 1e-6 {
		t.Errorf("steady-state b = %v, want ~0.5", ss["b"])
	}
}

func TestMarkov_Predict_UnderConstrainedChainReturnsZero(t *testing.T) {
	// Single-observation chain has the state but no transitions.
	chain := FitMarkov([]string{"only"})
	if p := chain.Predict("only", "only", 3); p != 0 {
		// An "only" state with no outgoing transitions stays
		// absorbing-but-empty in the empirical sense. Zero-mass
		// after one step is the honest answer.
		t.Errorf("under-determined predict = %v, want 0", p)
	}
}

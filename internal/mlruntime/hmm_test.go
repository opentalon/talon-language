package mlruntime

import (
	"math"
	"testing"
)

// Classic fair-vs-loaded-die HMM, used in HMM textbooks. Two
// hidden states: F (fair die) and L (loaded). Six observations
// (1-6). The fair die emits uniformly; the loaded die emits 6
// half the time. Transitions stay in the same state most of the
// time with a small chance of switching.
func textbookDieHMM() HMM {
	return HMM{
		States:       []string{"F", "L"},
		Observations: []string{"1", "2", "3", "4", "5", "6"},
		Initial:      []float64{0.5, 0.5},
		Trans: [][]float64{
			{0.95, 0.05},
			{0.10, 0.90},
		},
		Emit: [][]float64{
			{1.0 / 6, 1.0 / 6, 1.0 / 6, 1.0 / 6, 1.0 / 6, 1.0 / 6}, // F
			{0.10, 0.10, 0.10, 0.10, 0.10, 0.50},                    // L
		},
	}
}

func TestHMM_LogLikelihood_FairSequence(t *testing.T) {
	h := textbookDieHMM()
	// Uniform-looking sequence: each symbol once.
	ll := h.LogLikelihood([]string{"1", "2", "3", "4", "5", "6"})
	if math.IsInf(ll, -1) || math.IsNaN(ll) {
		t.Fatalf("ll = %v, expected finite", ll)
	}
}

func TestHMM_LogLikelihood_LoadedSequence(t *testing.T) {
	h := textbookDieHMM()
	// All 6s — should score higher than uniform under this model
	// because the loaded state emits 6 with prob 0.5.
	fair := h.LogLikelihood([]string{"1", "2", "3", "4", "5", "6"})
	loaded := h.LogLikelihood([]string{"6", "6", "6", "6", "6", "6"})
	if loaded <= fair {
		t.Errorf("expected all-6s sequence to have higher LL than uniform; got loaded=%v fair=%v", loaded, fair)
	}
}

func TestHMM_LogLikelihood_UnknownSymbol(t *testing.T) {
	h := textbookDieHMM()
	if ll := h.LogLikelihood([]string{"7"}); !math.IsInf(ll, -1) {
		t.Errorf("unknown symbol LL = %v, want -Inf", ll)
	}
}

func TestHMM_AnomalyScore_Healthy(t *testing.T) {
	h := textbookDieHMM()
	// Pick a generous cutoff so a normal-ish sequence scores 0
	// anomaly.
	ll := h.LogLikelihood([]string{"3", "1", "4", "2", "5"})
	score := h.AnomalyScore([]string{"3", "1", "4", "2", "5"}, ll)
	if score > 0 {
		t.Errorf("healthy sequence at cutoff = LL should score 0, got %v", score)
	}
}

func TestHMM_AnomalyScore_ImpossibleSequenceMaxesOut(t *testing.T) {
	h := textbookDieHMM()
	if s := h.AnomalyScore([]string{"unknown_symbol"}, -3); s != 1 {
		t.Errorf("impossible sequence anomaly = %v, want 1", s)
	}
}

func TestHMM_AnomalyScore_Gap(t *testing.T) {
	h := textbookDieHMM()
	seq := []string{"6", "6", "6"}
	ll := h.LogLikelihood(seq)
	// Cutoff 5 nats above true LL → gap 5 → score 0.5.
	score := h.AnomalyScore(seq, ll+5)
	if math.Abs(score-0.5) > 1e-9 {
		t.Errorf("score with 5-nat gap = %v, want 0.5", score)
	}
}

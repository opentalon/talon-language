package executor

import (
	"testing"
)

// TestProbabilisticGate_Distribution verifies the gating samples the
// right fraction over many synthetic rows. Not a chi-squared test,
// just a sanity check that the deterministic RNG is doing what the
// docstring claims.
func TestProbabilisticGate_Distribution(t *testing.T) {
	const n = 10000
	rows := make([][]any, n)
	for i := range rows {
		rows[i] = []any{float64(i)}
	}

	cases := []struct {
		prob    float64
		minKept int
		maxKept int
	}{
		{0.2, 1800, 2200},
		{0.5, 4800, 5200},
		{0.8, 7800, 8200},
	}
	for _, c := range cases {
		ex := &Executor{RandSeed: 42}
		out := ex.probabilisticGate(rows, c.prob, "test_block")
		kept, ok := out.([][]any)
		if !ok {
			t.Fatalf("p=%v: gate returned %T", c.prob, out)
		}
		if len(kept) < c.minKept || len(kept) > c.maxKept {
			t.Errorf("p=%v: kept %d, want [%d, %d]", c.prob, len(kept), c.minKept, c.maxKept)
		}
	}
}

// TestProbabilisticGate_Determinism: same seed → same output.
func TestProbabilisticGate_Determinism(t *testing.T) {
	rows := make([][]any, 100)
	for i := range rows {
		rows[i] = []any{float64(i)}
	}
	ex1 := &Executor{RandSeed: 7}
	ex2 := &Executor{RandSeed: 7}
	out1 := ex1.probabilisticGate(rows, 0.5, "same_block")
	out2 := ex2.probabilisticGate(rows, 0.5, "same_block")
	a, _ := out1.([][]any)
	b, _ := out2.([][]any)
	if len(a) != len(b) {
		t.Fatalf("nondeterministic: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i][0] != b[i][0] {
			t.Errorf("row %d diverges: %v vs %v", i, a[i], b[i])
		}
	}
}

// TestProbabilisticGate_ZeroSeedBlockSpecific: empty RandSeed still
// produces a stable result keyed on block name; different blocks
// diverge.
func TestProbabilisticGate_ZeroSeedBlockSpecific(t *testing.T) {
	rows := make([][]any, 100)
	for i := range rows {
		rows[i] = []any{float64(i)}
	}
	ex := &Executor{} // RandSeed = 0
	a, _ := ex.probabilisticGate(rows, 0.5, "block_a").([][]any)
	b, _ := ex.probabilisticGate(rows, 0.5, "block_b").([][]any)
	// Same seed across blocks would produce identical kept rows; the
	// per-block FNV hash should make them diverge in at least a few
	// positions over a 100-row, p=0.5 sample.
	diffs := 0
	for i := range a {
		// Compare positions actually present in both
		if i >= len(b) {
			break
		}
		if a[i][0] != b[i][0] {
			diffs++
		}
	}
	if diffs == 0 && len(a) == len(b) {
		t.Errorf("block_a and block_b produced identical sequences with RandSeed=0 — per-block seeding broken")
	}
}

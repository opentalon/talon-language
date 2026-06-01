package mlruntime

import (
	"context"
	"math"
	"testing"
)

// TestGrubbs_DetectsSingleOutlier — 8 values tightly clustered around 50 with
// one extreme at 250. Grubbs at α=0.05 must flag only the 250.
func TestGrubbs_DetectsSingleOutlier(t *testing.T) {
	in := Input{
		Rows: [][]any{
			{1, 48.0}, {2, 51.0}, {3, 49.0}, {4, 50.0},
			{5, 49.0}, {6, 50.0}, {7, 51.0}, {8, 250.0},
		},
	}
	prim := NewGrubbsAnomaly()
	results, err := prim.Compute(context.Background(), in)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	flagged := flaggedIDs(results)
	if len(flagged) != 1 || flagged[0] != 8 {
		t.Errorf("flagged: want [8], got %v", flagged)
	}
}

// TestGrubbs_NoOutlierInTightCluster — uniform values, nothing flagged.
func TestGrubbs_NoOutlierInTightCluster(t *testing.T) {
	in := Input{
		Rows: [][]any{
			{1, 50.0}, {2, 50.0}, {3, 50.0}, {4, 50.0},
			{5, 50.0}, {6, 50.0},
		},
	}
	prim := NewGrubbsAnomaly()
	results, _ := prim.Compute(context.Background(), in)
	if len(flaggedIDs(results)) != 0 {
		t.Errorf("uniform cluster: want 0 flagged, got %v", flaggedIDs(results))
	}
}

// TestGrubbs_AlphaTightening — at α=0.01 the test should be stricter than
// α=0.05. Fixture constructed so the outlier's G ≈ 2.26 falls in the
// narrow band between G_crit(8, 0.05)=2.126 and G_crit(8, 0.01)=2.274 —
// flagged under 0.05, NOT flagged under 0.01.
func TestGrubbs_AlphaTightening(t *testing.T) {
	rows := [][]any{
		{1, 45.0}, {2, 48.0}, {3, 50.0}, {4, 52.0},
		{5, 49.0}, {6, 51.0}, {7, 47.0}, {8, 63.0}, // borderline at α≈0.025
	}
	prim := NewGrubbsAnomaly()

	lax, _ := prim.Compute(context.Background(), Input{Rows: rows, Params: map[string]any{"alpha": 0.05}})
	strict, _ := prim.Compute(context.Background(), Input{Rows: rows, Params: map[string]any{"alpha": 0.01}})

	laxFlagged := flaggedIDs(lax)
	strictFlagged := flaggedIDs(strict)
	if !contains(laxFlagged, 8) {
		t.Errorf("expected α=0.05 to flag the borderline outlier (id 8); got %v", laxFlagged)
	}
	if contains(strictFlagged, 8) {
		t.Errorf("expected α=0.01 to NOT flag the borderline outlier (id 8); got %v", strictFlagged)
	}
}

// TestGrubbs_StricterThanZscoreOnSmallSamples — for n=4 the z-score
// threshold of 2.5 is unreachable (max |z| is bounded by (n-1)/sqrt(n) ≈ 1.5),
// so z-score never flags anything. Grubbs at α=0.05 should still flag a
// genuine outlier when one exists.
func TestGrubbs_HandlesSmallSamplesZscoreCannot(t *testing.T) {
	rows := [][]any{
		{1, 10.0}, {2, 11.0}, {3, 10.5}, {4, 10.3},
	}
	prim := NewGrubbsAnomaly()
	results, err := prim.Compute(context.Background(), Input{Rows: rows})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	// No outlier here — all values are tight. Should not flag.
	if len(flaggedIDs(results)) != 0 {
		t.Errorf("tight n=4 sample: want 0 flagged, got %v", flaggedIDs(results))
	}

	// Add a clear outlier at 100.
	rows = append(rows, []any{5, 100.0})
	results, _ = prim.Compute(context.Background(), Input{Rows: rows})
	flagged := flaggedIDs(results)
	if len(flagged) != 1 || flagged[0] != 5 {
		t.Errorf("n=5 with one outlier: want [5], got %v", flagged)
	}
}

// TestGrubbs_CriticalValueSanityCheck — published tables list G_crit(8, 0.05)
// ≈ 2.126. Our closed-form should match within ~3%.
func TestGrubbsCriticalValueSanity(t *testing.T) {
	cases := []struct {
		n     int
		alpha float64
		want  float64
		tol   float64
	}{
		{8, 0.05, 2.126, 0.05},
		{10, 0.05, 2.290, 0.05},
		{20, 0.05, 2.709, 0.10},
		{50, 0.05, 3.128, 0.15},
	}
	for _, c := range cases {
		got := grubbsCritical(c.n, c.alpha)
		if math.Abs(got-c.want) > c.tol {
			t.Errorf("grubbsCritical(%d, %v): want ≈ %v, got %v (diff %v > tol %v)",
				c.n, c.alpha, c.want, got, math.Abs(got-c.want), c.tol)
		}
	}
}

// TestGrubbs_SampleTooSmall — n < 3 yields an error.
func TestGrubbs_SampleTooSmall(t *testing.T) {
	prim := NewGrubbsAnomaly()
	_, err := prim.Compute(context.Background(), Input{Rows: [][]any{{1, 10.0}, {2, 20.0}}})
	if err == nil {
		t.Fatal("expected error for n=2, got nil")
	}
}

func flaggedIDs(results []Result) []int {
	out := []int{}
	for _, r := range results {
		if v, _ := r.Value.(bool); v {
			out = append(out, r.EntityID)
		}
	}
	return out
}

func contains(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

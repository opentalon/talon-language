package mlmodel

import "testing"

func sampleModel() *Model {
	return &Model{
		Name: "failure_risk", Algo: "classify_knn", K: 3,
		Features: []string{"km", "age"},
		Examples: []Example{
			{Features: map[string]float64{"km": 50000, "age": 8}, Label: "high"},
			{Features: map[string]float64{"km": 10000, "age": 2}, Label: "low"},
		},
	}
}

func TestTrainingRowsFromExamples(t *testing.T) {
	rows := sampleModel().TrainingRows()
	if len(rows) != 2 {
		t.Fatalf("want 2 training rows, got %d", len(rows))
	}
	if rows[0].Label != "high" || rows[0].Attrs["km"] != float64(50000) {
		t.Fatalf("row 0 mismatch: %+v", rows[0])
	}
	// Synthetic ids are negative so they never collide with real entities.
	if rows[0].ID >= 0 || rows[1].ID >= 0 {
		t.Fatalf("training ids should be negative, got %d, %d", rows[0].ID, rows[1].ID)
	}
}

// TestResolverPrefersTlnThenGo: a name present in both providers resolves to
// the tln model; a name only in the Go registry resolves to "go".
func TestResolverPrefersTlnThenGo(t *testing.T) {
	tlnM := sampleModel()
	goM := sampleModel()
	goM.K = 99 // make it distinguishable

	goReg := NewRegistry()
	goReg.Register("fleet.ml.failure_risk", goM)
	goReg.Register("vendor.ml.churn", goM)

	r := NewResolver(map[string]*Model{"fleet.ml.failure_risk": tlnM}, goReg)

	// tln wins the shared name.
	m, provider, ok := r.Resolve("fleet.ml.failure_risk")
	if !ok || provider != "tln" || m.K != 3 {
		t.Fatalf("shared name should resolve to tln (k=3), got ok=%v provider=%q k=%d", ok, provider, m.K)
	}
	// Go-only name resolves to the Go provider.
	m, provider, ok = r.Resolve("vendor.ml.churn")
	if !ok || provider != "go" || m.K != 99 {
		t.Fatalf("go-only name should resolve to Go (k=99), got ok=%v provider=%q", ok, provider)
	}
	// Unknown name.
	if _, _, ok := r.Resolve("nope.nope"); ok {
		t.Fatal("unknown model should not resolve")
	}
}

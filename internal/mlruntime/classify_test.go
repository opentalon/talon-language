package mlruntime

import (
	"context"
	"testing"
)

// train builds a labeled example with two numeric features.
func train(id int, f1, f2 float64, label string) TrainingRow {
	return TrainingRow{ID: id, Label: label, Attrs: map[string]any{"f1": f1, "f2": f2}}
}

// classifyInput assembles an Input for the kNN primitive: candidate ids in
// Rows, their features in Entities, labeled examples in Training.
func classifyInput(cands map[int][2]float64, training []TrainingRow, k int) Input {
	rows := make([][]any, 0, len(cands))
	ents := map[int]map[string]any{}
	for id, xy := range cands {
		rows = append(rows, []any{id})
		ents[id] = map[string]any{"f1": xy[0], "f2": xy[1]}
	}
	return Input{
		Rows:     rows,
		Entities: ents,
		Training: training,
		Params:   map[string]any{"feature_names": []string{"f1", "f2"}, "k": k},
	}
}

func classOf(t *testing.T, results []Result, id int) (string, float64) {
	t.Helper()
	for _, r := range results {
		if r.EntityID == id {
			cls, _ := r.Value.(string)
			return cls, r.Explanation.Confidence
		}
	}
	t.Fatalf("no result for entity %d", id)
	return "", 0
}

// TestClassifyTwoClusters is the issue's golden case: two clearly separated
// labeled groups; a candidate sitting on top of each group must take that
// group's label with full confidence at k = cluster size.
func TestClassifyTwoClusters(t *testing.T) {
	training := []TrainingRow{
		train(1, 10, 10, "hot"), train(2, 11, 9, "hot"), train(3, 9, 11, "hot"),
		train(4, 0, 0, "cold"), train(5, 1, 1, "cold"), train(6, -1, 0, "cold"),
	}
	in := classifyInput(map[int][2]float64{
		100: {10, 10}, // squarely in the hot cluster
		101: {0, 0},   // squarely in the cold cluster
	}, training, 3)

	results, err := NewKNNClassifier().Compute(context.Background(), in)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if cls, conf := classOf(t, results, 100); cls != "hot" || conf != 1.0 {
		t.Errorf("entity 100: got %q conf %v, want hot 1.0", cls, conf)
	}
	if cls, conf := classOf(t, results, 101); cls != "cold" || conf != 1.0 {
		t.Errorf("entity 101: got %q conf %v, want cold 1.0", cls, conf)
	}
}

// TestClassifyBorderlineConfidence — a candidate midway between the clusters
// still gets a class, but with a split (< 1.0) vote, which is exactly the
// signal a `confidence >= N` bound uses to drop uncertain predictions.
func TestClassifyBorderlineConfidence(t *testing.T) {
	training := []TrainingRow{
		train(1, 10, 10, "hot"), train(2, 10, 10, "hot"),
		train(3, 0, 0, "cold"), train(4, 0, 0, "cold"), train(5, 0, 0, "cold"),
	}
	// Candidate closer to cold but within reach of hot; k=5 polls everyone,
	// so the 3 cold beat the 2 hot: "cold" at 0.6.
	in := classifyInput(map[int][2]float64{200: {4, 4}}, training, 5)
	results, err := NewKNNClassifier().Compute(context.Background(), in)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	cls, conf := classOf(t, results, 200)
	if cls != "cold" {
		t.Errorf("entity 200: got class %q, want cold", cls)
	}
	if conf != 0.6 {
		t.Errorf("entity 200: got confidence %v, want 0.6 (3 of 5)", conf)
	}
}

// TestClassifyFeatureScaling proves the per-feature normalisation: without it,
// f2 (values in the thousands) would swamp f1 and every candidate would be
// classified by f2 alone. Here the true signal is in f1; f2 is constant noise
// at a huge magnitude.
func TestClassifyFeatureScaling(t *testing.T) {
	training := []TrainingRow{
		train(1, 10, 5000, "high"), train(2, 11, 5000, "high"),
		train(3, 0, 5000, "low"), train(4, 1, 5000, "low"),
	}
	in := classifyInput(map[int][2]float64{300: {10, 5000}}, training, 2)
	results, err := NewKNNClassifier().Compute(context.Background(), in)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if cls, _ := classOf(t, results, 300); cls != "high" {
		t.Errorf("entity 300: got %q, want high (f1 signal must survive f2's scale)", cls)
	}
}

// TestClassifyTieBreaksLexically — an even split resolves to the lexically
// smaller label, deterministically, regardless of map iteration order.
func TestClassifyTieBreaks(t *testing.T) {
	training := []TrainingRow{
		train(1, 0, 0, "zebra"), train(2, 0, 0, "apple"),
	}
	in := classifyInput(map[int][2]float64{400: {0, 0}}, training, 2)
	results, err := NewKNNClassifier().Compute(context.Background(), in)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if cls, _ := classOf(t, results, 400); cls != "apple" {
		t.Errorf("tie: got %q, want apple (lexical tiebreak)", cls)
	}
}

// TestClassifyEmptyTraining degrades to no predictions (not an error) so a
// `talon run` over a classify block whose training set is empty doesn't abort.
func TestClassifyEmptyTraining(t *testing.T) {
	in := classifyInput(map[int][2]float64{500: {1, 1}}, nil, 3)
	results, err := NewKNNClassifier().Compute(context.Background(), in)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("empty training: got %d results, want 0", len(results))
	}
}

// TestClassifyNoFeatures is a misconfiguration — the primitive rejects it.
func TestClassifyNoFeatures(t *testing.T) {
	_, err := NewKNNClassifier().Compute(context.Background(), Input{
		Rows:     [][]any{{1}},
		Training: []TrainingRow{train(1, 1, 1, "x")},
		Params:   map[string]any{},
	})
	if err == nil {
		t.Fatal("expected an error when no features are configured")
	}
}

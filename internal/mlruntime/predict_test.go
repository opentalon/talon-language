package mlruntime

import (
	"context"
	"testing"
)

// tsample builds a labeled training row with two numeric features.
func tsample(f1, f2 float64, label string) TrainingRow {
	return TrainingRow{Attrs: map[string]any{"f1": f1, "f2": f2}, Label: label}
}

// predictInput assembles an Input for the tree: candidate ids in Rows, their
// features in Entities, labeled examples in Training.
func predictInput(cands map[int][2]float64, training []TrainingRow, maxDepth, minLeaf int) Input {
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
		Params: map[string]any{
			"feature_names":    []string{"f1", "f2"},
			"max_depth":        maxDepth,
			"min_samples_leaf": minLeaf,
		},
	}
}

func predictOf(t *testing.T, results []Result, id int) (string, float64) {
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

// TestPredictLinearlySeparable — a single-feature threshold cleanly separates
// the classes; the tree must find it and classify candidates on either side.
func TestPredictLinearlySeparable(t *testing.T) {
	var training []TrainingRow
	for i := 0; i < 6; i++ {
		training = append(training, tsample(float64(i), 0, "low"))     // f1 0..5
		training = append(training, tsample(float64(i+10), 0, "high")) // f1 10..15
	}
	in := predictInput(map[int][2]float64{
		1: {2, 0},  // clearly low
		2: {13, 0}, // clearly high
	}, training, 5, 1)

	results, err := NewDecisionTreePredictor().Compute(context.Background(), in)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if cls, conf := predictOf(t, results, 1); cls != "low" || conf != 1.0 {
		t.Errorf("entity 1: got %q conf %v, want low 1.0", cls, conf)
	}
	if cls, conf := predictOf(t, results, 2); cls != "high" || conf != 1.0 {
		t.Errorf("entity 2: got %q conf %v, want high 1.0", cls, conf)
	}
}

// TestPredictXOR — the classic non-linearly-separable case. No single split
// works, but a depth-2 tree solves it: class is "on" iff exactly one of the
// two features is high. Proves the recursive splitting actually recurses.
func TestPredictXOR(t *testing.T) {
	var training []TrainingRow
	// Four corners, repeated so each quadrant clears min_samples_leaf.
	for i := 0; i < 4; i++ {
		training = append(training, tsample(0, 0, "off"))
		training = append(training, tsample(10, 10, "off"))
		training = append(training, tsample(0, 10, "on"))
		training = append(training, tsample(10, 0, "on"))
	}
	in := predictInput(map[int][2]float64{
		1: {0, 0},   // off
		2: {10, 10}, // off
		3: {0, 10},  // on
		4: {10, 0},  // on
	}, training, 3, 2)

	results, err := NewDecisionTreePredictor().Compute(context.Background(), in)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	for id, want := range map[int]string{1: "off", 2: "off", 3: "on", 4: "on"} {
		if cls, _ := predictOf(t, results, id); cls != want {
			t.Errorf("entity %d: got %q, want %q (XOR needs depth 2)", id, cls, want)
		}
	}
}

// TestPredictDecisionPath — the explanation carries the splits taken, so a
// reviewer can read *why* a row was classified. This is the whole point of a
// tree over an opaque model.
func TestPredictDecisionPath(t *testing.T) {
	var training []TrainingRow
	for i := 0; i < 6; i++ {
		training = append(training, tsample(float64(i), 0, "low"))
		training = append(training, tsample(float64(i+10), 0, "high"))
	}
	in := predictInput(map[int][2]float64{1: {13, 0}}, training, 5, 1)
	results, err := NewDecisionTreePredictor().Compute(context.Background(), in)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(results[0].Explanation.Rules) == 0 {
		t.Fatal("expected a non-empty decision path in the explanation")
	}
	// The discriminating split is on f1; the high candidate took a `>` branch.
	r := results[0].Explanation.Rules[0]
	if r.Attr != "f1" || r.Op != ">" {
		t.Errorf("first split: got %s %s %v, want f1 > …", r.Attr, r.Op, r.Value)
	}
}

// TestPredictMinSamplesLeafPreventsOverfit — one noisy point sits inside the
// other class's region. With min_samples_leaf=3 the tree can't carve out a
// leaf for that single outlier, so a candidate next to it takes the
// surrounding majority class rather than the noise label.
func TestPredictMinSamplesLeafPreventsOverfit(t *testing.T) {
	var training []TrainingRow
	// A dense "clean" block, plus a single mislabeled outlier at (5,5).
	for i := 0; i < 6; i++ {
		training = append(training, tsample(5, float64(i), "clean"))
	}
	training = append(training, tsample(5, 5, "noise")) // the outlier
	for i := 0; i < 6; i++ {
		training = append(training, tsample(50, float64(i), "far"))
	}

	in := predictInput(map[int][2]float64{1: {5, 5}}, training, 5, 3)
	results, err := NewDecisionTreePredictor().Compute(context.Background(), in)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if cls, _ := predictOf(t, results, 1); cls == "noise" {
		t.Errorf("entity 1: got %q — min_samples_leaf should have absorbed the single outlier", cls)
	}
}

// TestPredictEmptyTraining degrades to no predictions rather than erroring.
func TestPredictEmptyTraining(t *testing.T) {
	in := predictInput(map[int][2]float64{1: {1, 1}}, nil, 5, 3)
	results, err := NewDecisionTreePredictor().Compute(context.Background(), in)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("empty training: got %d results, want 0", len(results))
	}
}

// TestPredictNoFeatures is a misconfiguration — rejected.
func TestPredictNoFeatures(t *testing.T) {
	_, err := NewDecisionTreePredictor().Compute(context.Background(), Input{
		Rows:     [][]any{{1}},
		Training: []TrainingRow{tsample(1, 1, "x")},
		Params:   map[string]any{},
	})
	if err == nil {
		t.Fatal("expected an error when no features are configured")
	}
}

package mlruntime

import (
	"context"
	"errors"
	"math"
	"testing"
)

func makeRows(values []float64) [][]any {
	rows := make([][]any, len(values))
	for i, v := range values {
		rows[i] = []any{i + 1, v}
	}
	return rows
}

func TestZScoreClearOutlierFlagged(t *testing.T) {
	// 9 normal samples around 50, one outlier at 200 (z ~ 4 with this sample).
	values := []float64{48, 49, 50, 51, 52, 49, 50, 51, 50, 200}
	in := Input{Rows: makeRows(values)}

	got, err := NewZScoreAnomaly().Compute(context.Background(), in)
	if err != nil {
		t.Fatalf("Compute: unexpected error: %v", err)
	}
	if len(got) != len(values) {
		t.Fatalf("got %d results, want %d", len(got), len(values))
	}

	flagged := 0
	var outlier *Result
	for i := range got {
		if v, _ := got[i].Value.(bool); v {
			flagged++
			r := got[i]
			outlier = &r
		}
	}
	if flagged != 1 {
		t.Fatalf("expected exactly 1 flagged row, got %d", flagged)
	}
	if outlier.EntityID != 10 {
		t.Errorf("flagged entity: got %d, want 10", outlier.EntityID)
	}
	if outlier.Explanation.Threshold == nil || outlier.Explanation.Threshold.Method != "mean_stddev" {
		t.Errorf("expected threshold.method=mean_stddev, got %+v", outlier.Explanation.Threshold)
	}
	if outlier.Explanation.Threshold.Sample != len(values) {
		t.Errorf("threshold.sample: got %d, want %d", outlier.Explanation.Threshold.Sample, len(values))
	}
	if len(outlier.Explanation.Rules) != 1 || outlier.Explanation.Rules[0].Attr != "z_score" {
		t.Errorf("rules: got %+v", outlier.Explanation.Rules)
	}
}

func TestZScoreAllEqualSeriesProducesNoAnomalies(t *testing.T) {
	values := []float64{42, 42, 42, 42, 42}
	in := Input{Rows: makeRows(values)}

	got, err := NewZScoreAnomaly().Compute(context.Background(), in)
	if err != nil {
		t.Fatalf("Compute: unexpected error: %v", err)
	}
	for i, r := range got {
		if v, _ := r.Value.(bool); v {
			t.Errorf("row %d flagged but stddev=0; should be ignored", i)
		}
		if !math.IsNaN(r.Explanation.Inputs["z"].(float64)) {
			// z=0 acceptable; z=NaN not — divides by zero would yield NaN
			if z, _ := r.Explanation.Inputs["z"].(float64); math.IsNaN(z) || math.IsInf(z, 0) {
				t.Errorf("row %d: z is NaN/Inf on constant series", i)
			}
		}
	}
}

func TestZScoreSampleBelowMinimum(t *testing.T) {
	values := []float64{10, 20}
	in := Input{Rows: makeRows(values)}

	_, err := NewZScoreAnomaly().Compute(context.Background(), in)
	if !errors.Is(err, ErrSampleTooSmall) {
		t.Fatalf("expected ErrSampleTooSmall, got %v", err)
	}
}

func TestZScoreExplanationContainsObservedAndMean(t *testing.T) {
	values := []float64{10, 11, 12, 9, 10, 50}
	in := Input{Rows: makeRows(values)}

	got, err := NewZScoreAnomaly().Compute(context.Background(), in)
	if err != nil {
		t.Fatalf("Compute: unexpected error: %v", err)
	}
	last := got[len(got)-1]
	for _, key := range []string{"observed", "mean", "stddev", "z"} {
		if _, ok := last.Explanation.Inputs[key]; !ok {
			t.Errorf("explanation.inputs missing %q: %+v", key, last.Explanation.Inputs)
		}
	}
	if obs, _ := last.Explanation.Inputs["observed"].(float64); obs != 50 {
		t.Errorf("explanation observed: got %v, want 50", last.Explanation.Inputs["observed"])
	}
}

func TestZScoreCustomThresholdParam(t *testing.T) {
	// With default threshold 2.5 the value 60 against {50,50,50,50,50,60} has
	// z ~ 2.24 — NOT flagged. With threshold=2.0 it IS flagged.
	values := []float64{50, 50, 50, 50, 50, 60}
	in := Input{
		Rows:   makeRows(values),
		Params: map[string]any{"threshold": 2.0},
	}

	got, err := NewZScoreAnomaly().Compute(context.Background(), in)
	if err != nil {
		t.Fatalf("Compute: unexpected error: %v", err)
	}
	flagged := false
	for _, r := range got {
		if v, _ := r.Value.(bool); v {
			flagged = true
		}
	}
	if !flagged {
		t.Error("expected at least one row flagged with threshold=2.0")
	}

	// Same input, default threshold — no flag.
	in.Params = nil
	got, _ = NewZScoreAnomaly().Compute(context.Background(), in)
	for _, r := range got {
		if v, _ := r.Value.(bool); v {
			t.Error("expected no flag with default threshold 2.5")
		}
	}
}

func TestZScoreNonNumericRowsSkipped(t *testing.T) {
	rows := [][]any{
		{1, 10.0},
		{2, "oops"}, // non-numeric value column — skipped
		{3, 11.0},
		{4, 12.0},
		{5, 9.0},
		{6, 50.0},
	}
	in := Input{Rows: rows}

	got, err := NewZScoreAnomaly().Compute(context.Background(), in)
	if err != nil {
		t.Fatalf("Compute: unexpected error: %v", err)
	}
	// 5 numeric rows produce results; the string row is dropped.
	if len(got) != 5 {
		t.Fatalf("got %d results, want 5", len(got))
	}
	if got[0].Explanation.Threshold.Sample != 5 {
		t.Errorf("sample size: got %d, want 5", got[0].Explanation.Threshold.Sample)
	}
}

func TestZScoreSchemaOverridesColumnIndex(t *testing.T) {
	// Value in column 0, entity_id in column 1 — reversed from default.
	rows := [][]any{
		{10.0, 100},
		{11.0, 101},
		{12.0, 102},
		{9.0, 103},
		{200.0, 999},
	}
	in := Input{
		Rows:   rows,
		Schema: map[string]int{"value": 0, "entity_id": 1},
	}
	got, err := NewZScoreAnomaly().Compute(context.Background(), in)
	if err != nil {
		t.Fatalf("Compute: unexpected error: %v", err)
	}
	last := got[len(got)-1]
	if last.EntityID != 999 {
		t.Errorf("entity_id from schema-mapped column: got %d, want 999", last.EntityID)
	}
}

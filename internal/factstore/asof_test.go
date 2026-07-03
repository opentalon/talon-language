package factstore

import (
	"context"
	"testing"
	"time"
)

// statusIDs runs an as-of status query and returns the matched entity IDs.
func statusIDs(t *testing.T, m *MemoryStore, status string, at time.Time) []float64 {
	t.Helper()
	q := Query{
		Find:  []string{"?e"},
		Where: []Clause{&Pattern{Entity: Var("e"), Attribute: ":record/status", Value: Lit(status)}},
	}
	rows, err := m.QueryAsOf(context.Background(), q, at)
	if err != nil {
		t.Fatalf("QueryAsOf: %v", err)
	}
	out := make([]float64, 0, len(rows))
	for _, r := range rows {
		out = append(out, r[0].(float64))
	}
	return out
}

// TestQueryAsOfReconstructsPastValue proves that a value change is
// time-travelled: a record certified in the past but defective now
// matches the certified query only at the earlier instant.
func TestQueryAsOfReconstructsPastValue(t *testing.T) {
	m := NewMemoryStore()

	t0 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	m.SetClock(func() time.Time { return t0 })
	if err := m.Assert(context.Background(), []Fact{
		{RecordID: "1", Attribute: ":record/type", Value: "machine"},
		{RecordID: "1", Attribute: ":record/status", Value: "certified"},
	}); err != nil {
		t.Fatalf("assert v1: %v", err)
	}

	m.SetClock(func() time.Time { return t1 })
	if err := m.Assert(context.Background(), []Fact{
		{RecordID: "1", Attribute: ":record/status", Value: "defective"},
	}); err != nil {
		t.Fatalf("assert v2: %v", err)
	}

	mid := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	after := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	if got := statusIDs(t, m, "certified", mid); len(got) != 1 || got[0] != 1 {
		t.Fatalf("certified as-of mid = %v, want [1]", got)
	}
	if got := statusIDs(t, m, "defective", mid); len(got) != 0 {
		t.Fatalf("defective as-of mid = %v, want []", got)
	}
	if got := statusIDs(t, m, "certified", after); len(got) != 0 {
		t.Fatalf("certified as-of after = %v, want []", got)
	}
	if got := statusIDs(t, m, "defective", after); len(got) != 1 || got[0] != 1 {
		t.Fatalf("defective as-of after = %v, want [1]", got)
	}

	// Current-state Query is unaffected by the history machinery.
	rows, err := m.Query(context.Background(), Query{
		Find:  []string{"?e"},
		Where: []Clause{&Pattern{Entity: Var("e"), Attribute: ":record/status", Value: Lit("defective")}},
	})
	if err != nil || len(rows) != 1 {
		t.Fatalf("current Query = %v (err %v), want 1 row", rows, err)
	}
}

// TestQueryAsOfBeforeCreation returns nothing for a record that did not
// exist yet at the target instant.
func TestQueryAsOfBeforeCreation(t *testing.T) {
	m := NewMemoryStore()
	t0 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	m.SetClock(func() time.Time { return t0 })
	if err := m.Assert(context.Background(), []Fact{
		{RecordID: "1", Attribute: ":record/status", Value: "certified"},
	}); err != nil {
		t.Fatalf("assert: %v", err)
	}
	before := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := statusIDs(t, m, "certified", before); len(got) != 0 {
		t.Fatalf("as-of before creation = %v, want []", got)
	}
}

// TestQueryAsOfRetractedTombstone hides a cell retracted before the
// target instant but surfaces it beforehand.
func TestQueryAsOfRetractedTombstone(t *testing.T) {
	m := NewMemoryStore()
	t0 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	m.SetClock(func() time.Time { return t0 })
	if err := m.Assert(context.Background(), []Fact{
		{RecordID: "1", Attribute: ":record/status", Value: "certified"},
	}); err != nil {
		t.Fatalf("assert: %v", err)
	}

	m.SetClock(func() time.Time { return t1 })
	if err := m.Retract(context.Background(), RetractPattern{RecordID: "1", Attribute: ":record/status"}); err != nil {
		t.Fatalf("retract: %v", err)
	}

	mid := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	after := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	if got := statusIDs(t, m, "certified", mid); len(got) != 1 || got[0] != 1 {
		t.Fatalf("as-of before retract = %v, want [1]", got)
	}
	if got := statusIDs(t, m, "certified", after); len(got) != 0 {
		t.Fatalf("as-of after retract = %v, want []", got)
	}
}

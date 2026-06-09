package factstore

import (
	"context"
	"testing"
)

func TestRetractWholeEntity(t *testing.T) {
	m := newSeeded(t)
	if err := m.Retract(context.Background(), RetractPattern{RecordID: "501"}); err != nil {
		t.Fatalf("retract: %v", err)
	}
	snap := m.Snapshot()
	if _, ok := snap[501]; ok {
		t.Fatalf("entity 501 still present after whole-entity retract")
	}
	if _, ok := snap[502]; !ok {
		t.Fatalf("unrelated entity 502 was dropped")
	}
}

func TestRetractAttributeAnyValue(t *testing.T) {
	m := newSeeded(t)
	if err := m.Retract(context.Background(), RetractPattern{
		RecordID:  "501",
		Attribute: ":attr/km",
	}); err != nil {
		t.Fatalf("retract: %v", err)
	}
	snap := m.Snapshot()
	if _, ok := snap[501][":attr/km"]; ok {
		t.Fatalf(":attr/km still present after attribute retract")
	}
	if snap[501][":attr/name"] != "VW Transporter" {
		t.Fatalf("sibling attribute lost during attribute retract: %v", snap[501])
	}
}

func TestRetractAttributeSpecificValueMatches(t *testing.T) {
	m := newSeeded(t)
	if err := m.Retract(context.Background(), RetractPattern{
		RecordID:  "501",
		Attribute: ":attr/km",
		Value:     45000.0,
	}); err != nil {
		t.Fatalf("retract: %v", err)
	}
	snap := m.Snapshot()
	if _, ok := snap[501][":attr/km"]; ok {
		t.Fatalf(":attr/km still present after value-specific retract")
	}
}

func TestRetractAttributeSpecificValueMismatch(t *testing.T) {
	m := newSeeded(t)
	if err := m.Retract(context.Background(), RetractPattern{
		RecordID:  "501",
		Attribute: ":attr/km",
		Value:     99999.0,
	}); err != nil {
		t.Fatalf("retract: %v", err)
	}
	snap := m.Snapshot()
	if snap[501][":attr/km"] != 45000.0 {
		t.Fatalf("value-mismatch retract dropped cell anyway: %v", snap[501])
	}
}

func TestRetractMissingEntityIsIdempotent(t *testing.T) {
	m := newSeeded(t)
	if err := m.Retract(context.Background(), RetractPattern{RecordID: "9999"}); err != nil {
		t.Fatalf("retract of missing entity returned error: %v", err)
	}
	if m.Len() != 3 {
		t.Fatalf("retract of missing entity changed Len: got %d", m.Len())
	}
}

func TestRetractRequiresRecordID(t *testing.T) {
	m := newSeeded(t)
	if err := m.Retract(context.Background(), RetractPattern{}); err == nil {
		t.Fatalf("retract without RecordID should error")
	}
}

func TestRetractEmitsEventPerCell(t *testing.T) {
	m := newSeeded(t)
	var got []Event
	m.Events().Subscribe(func(ctx context.Context, ev Event) {
		got = append(got, ev)
	})
	if err := m.Retract(context.Background(), RetractPattern{RecordID: "501"}); err != nil {
		t.Fatalf("retract: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("want 4 retract events (one per cell), got %d: %+v", len(got), got)
	}
	for _, ev := range got {
		if ev.Kind != EventRetract {
			t.Fatalf("non-retract event kind: %s", ev.Kind)
		}
		if ev.Fact.RecordID != "501" {
			t.Fatalf("event for wrong record id: %s", ev.Fact.RecordID)
		}
	}
}

func TestRetractEmitsSingleEventForSingleCell(t *testing.T) {
	m := newSeeded(t)
	var got []Event
	m.Events().Subscribe(func(ctx context.Context, ev Event) {
		got = append(got, ev)
	})
	if err := m.Retract(context.Background(), RetractPattern{
		RecordID:  "501",
		Attribute: ":attr/km",
	}); err != nil {
		t.Fatalf("retract: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 retract event, got %d", len(got))
	}
	if got[0].Fact.Attribute != ":attr/km" {
		t.Fatalf("event attribute: %s", got[0].Fact.Attribute)
	}
}

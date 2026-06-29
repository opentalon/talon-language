package executor

import (
	"context"
	"strconv"
	"testing"

	"github.com/opentalon/talon-language/internal/factstore"
)

// emitRecord writes one timestamped record of the given type pointing
// at targetID via the `on` grouping attribute.
func emitRecord(t *testing.T, store *factstore.MemoryStore, recordID, targetID int, recType, on string, at float64) {
	t.Helper()
	idStr := strconv.Itoa(recordID)
	err := store.Assert(context.Background(), []factstore.Fact{
		{RecordID: idStr, Attribute: ":record/type", Value: recType},
		{RecordID: idStr, Attribute: ":record/" + on, Value: int64(targetID)},
		{RecordID: idStr, Attribute: ":record/at", Value: at},
	})
	if err != nil {
		t.Fatalf("emit record %d: %v", recordID, err)
	}
}

func TestRecordSequence_MatchInOrder(t *testing.T) {
	src := `
detect "Engine failure chain" {
  for records where type == "vehicle"
    and record type "electrical_fault" followed_by record type "engine_failure"
        on same item within 30 days
  flag matching items
  label "{item.name}: engine failure chain"
  priority HIGH
}
`
	plans := compileSrc(t, src)
	store := factstore.NewMemoryStore()
	ctx := context.Background()

	// Vehicle 1: fault then failure within 5 days → MATCH.
	_ = store.Assert(ctx, []factstore.Fact{
		{RecordID: "1", Attribute: ":record/type", Value: "vehicle"},
		{RecordID: "1", Attribute: ":attr/name", Value: "Truck A"},
	})
	emitRecord(t, store, 100, 1, "electrical_fault", "item", 1000)
	emitRecord(t, store, 101, 1, "engine_failure", "item", 1000+5*86400)

	// Vehicle 2: failure first, then fault → NO MATCH (wrong order).
	_ = store.Assert(ctx, []factstore.Fact{
		{RecordID: "2", Attribute: ":record/type", Value: "vehicle"},
		{RecordID: "2", Attribute: ":attr/name", Value: "Van B"},
	})
	emitRecord(t, store, 200, 2, "engine_failure", "item", 1000)
	emitRecord(t, store, 201, 2, "electrical_fault", "item", 2000)

	// Vehicle 3: right order but 31 days apart → outside window.
	_ = store.Assert(ctx, []factstore.Fact{
		{RecordID: "3", Attribute: ":record/type", Value: "vehicle"},
		{RecordID: "3", Attribute: ":attr/name", Value: "Car C"},
	})
	emitRecord(t, store, 300, 3, "electrical_fault", "item", 1000)
	emitRecord(t, store, 301, 3, "engine_failure", "item", 1000+31*86400)

	// Vehicle 4: only fault, no failure → NO MATCH.
	_ = store.Assert(ctx, []factstore.Fact{
		{RecordID: "4", Attribute: ":record/type", Value: "vehicle"},
		{RecordID: "4", Attribute: ":attr/name", Value: "Truck D"},
	})
	emitRecord(t, store, 400, 4, "electrical_fault", "item", 1000)

	ex := NewExecutor(store)
	blocks, err := ex.RunAll(ctx, plans)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	res := blocks["Engine failure chain"]
	if res == nil {
		t.Fatal("no block result")
	}
	if len(res.Flagged) != 1 {
		t.Fatalf("flagged = %v, want only [1]", res.Flagged)
	}
	id, ok := toIntSM(res.Flagged[0][0])
	if !ok || id != 1 {
		t.Fatalf("flagged[0] = %v, want vehicle 1", res.Flagged[0])
	}
}

func TestRecordSequence_UnboundedWindow(t *testing.T) {
	// No `within` clause → all in-order matches accepted regardless of span.
	src := `
detect "Ever a fault then a failure" {
  for records where type == "vehicle"
    and record type "electrical_fault" followed_by record type "engine_failure"
        on same item
  flag matching items
  label "{item.name}"
  priority HIGH
}
`
	plans := compileSrc(t, src)
	store := factstore.NewMemoryStore()
	ctx := context.Background()

	_ = store.Assert(ctx, []factstore.Fact{
		{RecordID: "1", Attribute: ":record/type", Value: "vehicle"},
		{RecordID: "1", Attribute: ":attr/name", Value: "Vintage"},
	})
	// Decades apart — must still match because no window declared.
	emitRecord(t, store, 10, 1, "electrical_fault", "item", 1000)
	emitRecord(t, store, 11, 1, "engine_failure", "item", 1000+365*86400*30)

	ex := NewExecutor(store)
	blocks, err := ex.RunAll(ctx, plans)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := len(blocks["Ever a fault then a failure"].Flagged); got != 1 {
		t.Fatalf("want 1 flagged, got %d", got)
	}
}

func TestRecordSequence_ThreeStepChain(t *testing.T) {
	// Three-step chain with explicit grouping key (vehicle, not item).
	src := `
detect "Fault chain" {
  for records where type == "fleet_vehicle"
    and record type "warning"
        followed_by record type "fault"
        followed_by record type "failure"
        on same vehicle within 60 days
  flag matching items
  label "{item.name}"
  priority HIGH
}
`
	plans := compileSrc(t, src)
	store := factstore.NewMemoryStore()
	ctx := context.Background()

	// Vehicle 7: full chain in 40 days → MATCH.
	_ = store.Assert(ctx, []factstore.Fact{
		{RecordID: "7", Attribute: ":record/type", Value: "fleet_vehicle"},
		{RecordID: "7", Attribute: ":attr/name", Value: "Bus 7"},
	})
	emitRecord(t, store, 70, 7, "warning", "vehicle", 1000)
	emitRecord(t, store, 71, 7, "fault", "vehicle", 1000+10*86400)
	emitRecord(t, store, 72, 7, "failure", "vehicle", 1000+40*86400)

	// Vehicle 8: missing middle step → NO MATCH.
	_ = store.Assert(ctx, []factstore.Fact{
		{RecordID: "8", Attribute: ":record/type", Value: "fleet_vehicle"},
		{RecordID: "8", Attribute: ":attr/name", Value: "Bus 8"},
	})
	emitRecord(t, store, 80, 8, "warning", "vehicle", 1000)
	emitRecord(t, store, 81, 8, "failure", "vehicle", 1000+20*86400)

	ex := NewExecutor(store)
	blocks, err := ex.RunAll(ctx, plans)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := len(blocks["Fault chain"].Flagged); got != 1 {
		t.Fatalf("want 1 flagged, got %d", got)
	}
}

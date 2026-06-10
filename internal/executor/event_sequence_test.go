package executor

import (
	"context"
	"strconv"
	"testing"

	"github.com/opentalon/talon-language/internal/factstore"
)

// emitEvent records one event for entity targetID.
func emitEvent(t *testing.T, store *factstore.MemoryStore, eventID, targetID int, name string, at float64) {
	t.Helper()
	idStr := strconv.Itoa(eventID)
	err := store.Assert(context.Background(), []factstore.Fact{
		{RecordID: idStr, Attribute: ":record/type", Value: "event"},
		{RecordID: idStr, Attribute: ":event/entity", Value: int64(targetID)},
		{RecordID: idStr, Attribute: ":event/name", Value: name},
		{RecordID: idStr, Attribute: ":event/at", Value: at},
	})
	if err != nil {
		t.Fatalf("emit event %d: %v", eventID, err)
	}
}

func TestEventSequence_MatchInOrder(t *testing.T) {
	src := `
detect "Cart abandonment" {
  for records where type == "user"
    and event_sequence "cart_opened" -> "item_added" -> "session_ended" within 7 days
  flag matching items
  label "{item.name}: abandoned cart"
  priority HIGH
}
`
	plans := compileSrc(t, src)
	store := factstore.NewMemoryStore()
	ctx := context.Background()

	// User #1 abandons cart in the right order, within window.
	_ = store.Assert(ctx, []factstore.Fact{
		{RecordID: "1", Attribute: ":record/type", Value: "user"},
		{RecordID: "1", Attribute: ":attr/name", Value: "Alice"},
	})
	emitEvent(t, store, 10, 1, "cart_opened", 1000)
	emitEvent(t, store, 11, 1, "item_added", 1500)
	emitEvent(t, store, 12, 1, "session_ended", 2000)

	// User #2 has events but out of order — should NOT match.
	_ = store.Assert(ctx, []factstore.Fact{
		{RecordID: "2", Attribute: ":record/type", Value: "user"},
		{RecordID: "2", Attribute: ":attr/name", Value: "Bob"},
	})
	emitEvent(t, store, 20, 2, "session_ended", 1000)
	emitEvent(t, store, 21, 2, "cart_opened", 1500)
	emitEvent(t, store, 22, 2, "item_added", 2000)

	// User #3 has the sequence but outside the 7-day window.
	_ = store.Assert(ctx, []factstore.Fact{
		{RecordID: "3", Attribute: ":record/type", Value: "user"},
		{RecordID: "3", Attribute: ":attr/name", Value: "Carol"},
	})
	emitEvent(t, store, 30, 3, "cart_opened", 1000)
	emitEvent(t, store, 31, 3, "item_added", 1500)
	emitEvent(t, store, 32, 3, "session_ended", 1000+7*86400+1) // 1 second past window

	ex := NewExecutor(store)
	blocks, err := ex.RunAll(ctx, plans)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	res := blocks["Cart abandonment"]
	if res == nil {
		t.Fatal("no block result")
	}
	// Only user #1 should be flagged.
	if len(res.Flagged) != 1 {
		t.Fatalf("flagged = %v, want only [1]", res.Flagged)
	}
	id, ok := toIntSM(res.Flagged[0][0])
	if !ok || id != 1 {
		t.Fatalf("flagged[0] = %v, want user 1", res.Flagged[0])
	}
}

func TestEventSequence_NoWindowMeansUnbounded(t *testing.T) {
	src := `
detect "Ever opened then ended" {
  for records where type == "user"
    and event_sequence "cart_opened" -> "session_ended"
  flag matching items
  label "{item.name}"
  priority HIGH
}
`
	plans := compileSrc(t, src)
	store := factstore.NewMemoryStore()
	ctx := context.Background()

	_ = store.Assert(ctx, []factstore.Fact{
		{RecordID: "1", Attribute: ":record/type", Value: "user"},
		{RecordID: "1", Attribute: ":attr/name", Value: "Alice"},
	})
	// Decades apart — must still match because no window declared.
	emitEvent(t, store, 10, 1, "cart_opened", 1000)
	emitEvent(t, store, 11, 1, "session_ended", 1000+365*86400*30)

	ex := NewExecutor(store)
	blocks, err := ex.RunAll(ctx, plans)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(blocks["Ever opened then ended"].Flagged) != 1 {
		t.Fatalf("want 1 flagged, got %d", len(blocks["Ever opened then ended"].Flagged))
	}
}

package factstore

import (
	"context"
	"reflect"
	"sync"
	"testing"
)

// collectingSubscriber returns a Subscriber that appends every event
// it receives into the returned slice; the slice and its mutex are
// returned for inspection.
func collectingSubscriber() (*sync.Mutex, *[]Event, Subscriber) {
	var mu sync.Mutex
	var got []Event
	return &mu, &got, func(_ context.Context, ev Event) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	}
}

func TestAssertNewFactEmitsAssert(t *testing.T) {
	m := NewMemoryStore()
	mu, got, sub := collectingSubscriber()
	m.Events().Subscribe(sub)

	want := Fact{RecordID: "501", Attribute: ":record/type", Value: "item"}
	if err := m.Assert(context.Background(), []Fact{want}); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 1 {
		t.Fatalf("got %d events, want 1: %v", len(*got), *got)
	}
	ev := (*got)[0]
	if ev.Kind != EventAssert {
		t.Errorf("Kind = %v, want EventAssert", ev.Kind)
	}
	if !reflect.DeepEqual(ev.Fact, want) {
		t.Errorf("Fact = %+v, want %+v", ev.Fact, want)
	}
	if (ev.Prev != Fact{}) {
		t.Errorf("Prev should be zero on assert, got %+v", ev.Prev)
	}
}

func TestAssertChangedValueEmitsChange(t *testing.T) {
	m := NewMemoryStore()
	mu, got, sub := collectingSubscriber()
	m.Events().Subscribe(sub)
	ctx := context.Background()

	first := Fact{RecordID: "501", Attribute: ":attr/km", Value: 45000.0}
	if err := m.Assert(ctx, []Fact{first}); err != nil {
		t.Fatalf("Assert 1: %v", err)
	}
	second := Fact{RecordID: "501", Attribute: ":attr/km", Value: 50000.0}
	if err := m.Assert(ctx, []Fact{second}); err != nil {
		t.Fatalf("Assert 2: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 2 {
		t.Fatalf("got %d events, want 2: %v", len(*got), *got)
	}
	if (*got)[0].Kind != EventAssert {
		t.Errorf("event[0] Kind = %v, want EventAssert", (*got)[0].Kind)
	}
	chg := (*got)[1]
	if chg.Kind != EventChange {
		t.Fatalf("event[1] Kind = %v, want EventChange", chg.Kind)
	}
	if !reflect.DeepEqual(chg.Fact, second) {
		t.Errorf("change.Fact = %+v, want new fact %+v", chg.Fact, second)
	}
	if !reflect.DeepEqual(chg.Prev, first) {
		t.Errorf("change.Prev = %+v, want first fact %+v", chg.Prev, first)
	}
}

func TestAssertIdempotentNoEvent(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()
	if err := m.Assert(ctx, []Fact{
		{RecordID: "501", Attribute: ":attr/km", Value: 45000.0},
	}); err != nil {
		t.Fatalf("Assert seed: %v", err)
	}

	mu, got, sub := collectingSubscriber()
	m.Events().Subscribe(sub)

	if err := m.Assert(ctx, []Fact{
		{RecordID: "501", Attribute: ":attr/km", Value: 45000.0},
	}); err != nil {
		t.Fatalf("Assert idempotent: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 0 {
		t.Fatalf("idempotent Assert fired %d events, want 0: %v", len(*got), *got)
	}
}

func TestAssertEmitsAfterMutationCommitted(t *testing.T) {
	// The subscriber MUST be able to observe the mutation it was just
	// notified about. If we emit while still holding the lock, a
	// subscriber that re-enters the store will deadlock.
	m := NewMemoryStore()
	observed := make(chan int, 1)
	m.Events().Subscribe(func(_ context.Context, ev Event) {
		if ev.Kind != EventAssert {
			return
		}
		// Re-enter the store: should not deadlock; should see the
		// fact we were just notified about.
		observed <- m.Len()
	})
	if err := m.Assert(context.Background(), []Fact{
		{RecordID: "501", Attribute: ":record/type", Value: "item"},
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if got := <-observed; got != 1 {
		t.Fatalf("subscriber saw Len = %d, want 1 (mutation should be committed before emit)", got)
	}
}

func TestAssertMultiFactBatchEventsInOrder(t *testing.T) {
	m := NewMemoryStore()
	mu, got, sub := collectingSubscriber()
	m.Events().Subscribe(sub)

	facts := []Fact{
		{RecordID: "501", Attribute: ":record/type", Value: "item"},
		{RecordID: "501", Attribute: ":attr/km", Value: 45000.0},
		{RecordID: "502", Attribute: ":record/type", Value: "item"},
	}
	if err := m.Assert(context.Background(), facts); err != nil {
		t.Fatalf("Assert: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 3 {
		t.Fatalf("got %d events, want 3: %v", len(*got), *got)
	}
	for i, ev := range *got {
		if ev.Kind != EventAssert {
			t.Errorf("event[%d] Kind = %v, want EventAssert", i, ev.Kind)
		}
		if !reflect.DeepEqual(ev.Fact, facts[i]) {
			t.Errorf("event[%d] Fact = %+v, want %+v", i, ev.Fact, facts[i])
		}
	}
}

func TestAssertEmptyAttributeNoEvent(t *testing.T) {
	// Empty attribute is the "entity declaration with no payload"
	// pattern; existing Assert ignores it. Make sure it still emits no
	// event so reactive dispatchers don't see phantom asserts.
	m := NewMemoryStore()
	mu, got, sub := collectingSubscriber()
	m.Events().Subscribe(sub)
	if err := m.Assert(context.Background(), []Fact{
		{RecordID: "501", Attribute: "", Value: nil},
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*got) != 0 {
		t.Fatalf("empty-attribute Assert fired %d events, want 0: %v", len(*got), *got)
	}
}

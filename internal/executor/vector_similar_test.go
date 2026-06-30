package executor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/opentalon/talon-language/internal/factstore"
	"github.com/opentalon/talon-language/internal/planner"
)

// stubVectorBackend captures the args of the last VectorSearch call and
// returns a canned hit list. ID-only — the executor never looks at
// distance once Within is unset.
type stubVectorBackend struct {
	lastScope string
	lastVec   []float32
	lastK     int
	hits      []VectorHit
	err       error
}

func (s *stubVectorBackend) VectorSearch(_ context.Context, scope string, vec []float32, k int) ([]VectorHit, error) {
	s.lastScope = scope
	s.lastVec = append([]float32(nil), vec...)
	s.lastK = k
	return s.hits, s.err
}


func TestVectorSimilarFiltersAndExcludesSeed(t *testing.T) {
	t.Parallel()
	src := `
find similar "Find related vehicles" {
  for records where type == "vehicle"
  to 1
  using vector scope "embed3"
  top 3
}
`
	plans := compileSrc(t, src)

	store := factstore.NewMemoryStore()
	ctx := context.Background()
	// Seed vehicles 1..4. Vehicle 1 carries the seed vector.
	for _, f := range []factstore.Fact{
		{RecordID: "1", Attribute: ":record/type", Value: "vehicle"},
		{RecordID: "1", Attribute: ":vector/embed3", Value: []float64{1, 0, 0}},
		{RecordID: "2", Attribute: ":record/type", Value: "vehicle"},
		{RecordID: "3", Attribute: ":record/type", Value: "vehicle"},
		{RecordID: "4", Attribute: ":record/type", Value: "vehicle"},
	} {
		if err := store.Assert(ctx, []factstore.Fact{f}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	stub := &stubVectorBackend{hits: []VectorHit{
		{ID: "1", Distance: 0},   // seed — must be filtered out
		{ID: "2", Distance: 0.1},
		{ID: "3", Distance: 0.2},
		{ID: "4", Distance: 0.3}, // dropped by top=3 after excluding seed
	}}
	ex := NewExecutor(store)
	ex.VectorBackend = stub
	blocks, err := ex.RunAll(ctx, plans)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	res := blocks["Find related vehicles"]
	if res == nil {
		t.Fatal("no block result")
	}
	if len(res.Flagged) != 3 {
		t.Fatalf("flagged = %v, want 3 rows (2, 3, 4 minus seed 1)", res.Flagged)
	}
	if stub.lastScope != "embed3" {
		t.Errorf("backend scope = %q, want %q", stub.lastScope, "embed3")
	}
	// Executor over-fetches by 1 (TopK+1) so it can drop the seed.
	if stub.lastK != 4 {
		t.Errorf("backend k = %d, want %d", stub.lastK, 4)
	}
	// Forwarded vector matches the seed's stored vector.
	if len(stub.lastVec) != 3 || stub.lastVec[0] != 1 {
		t.Errorf("seed vector forwarded incorrectly: %v", stub.lastVec)
	}
	for _, row := range res.Flagged {
		if id, _ := row[0].(int64); id == 1 {
			t.Errorf("seed leaked through: %v", row)
		}
	}
}

func TestVectorSimilarWithinThreshold(t *testing.T) {
	// Within 0.15 keeps only the closest neighbour; the distance-0.2
	// hit must be dropped even though it's inside top-K.
	t.Parallel()
	src := `
find similar "Tight neighbours" {
  for records where type == "vehicle"
  to 1
  using vector scope "embed3"
  top 5
  within 0.15
}
`
	plans := compileSrc(t, src)

	store := factstore.NewMemoryStore()
	ctx := context.Background()
	_ = store.Assert(ctx, []factstore.Fact{
		{RecordID: "1", Attribute: ":record/type", Value: "vehicle"},
		{RecordID: "1", Attribute: ":vector/embed3", Value: []float64{1, 0, 0}},
		{RecordID: "2", Attribute: ":record/type", Value: "vehicle"},
		{RecordID: "3", Attribute: ":record/type", Value: "vehicle"},
	})

	stub := &stubVectorBackend{hits: []VectorHit{
		{ID: "1", Distance: 0},
		{ID: "2", Distance: 0.1},
		{ID: "3", Distance: 0.2},
	}}
	ex := NewExecutor(store)
	ex.VectorBackend = stub
	blocks, err := ex.RunAll(ctx, plans)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := len(blocks["Tight neighbours"].Flagged); got != 1 {
		t.Errorf("flagged = %d, want 1 (only 2 is inside 0.15)", got)
	}
}

func TestVectorSimilarNoBackendIsExplicitError(t *testing.T) {
	t.Parallel()
	src := `
find similar "Backendless" {
  for records where type == "vehicle"
  to 1
  using vector scope "embed3"
}
`
	plans := compileSrc(t, src)

	store := factstore.NewMemoryStore()
	ctx := context.Background()
	_ = store.Assert(ctx, []factstore.Fact{
		{RecordID: "1", Attribute: ":record/type", Value: "vehicle"},
		{RecordID: "1", Attribute: ":vector/embed3", Value: []float64{1, 0, 0}},
	})

	ex := NewExecutor(store) // VectorBackend left nil
	_, err := ex.RunAll(ctx, plans)
	if !errors.Is(err, ErrNoVectorBackend) {
		t.Errorf("want ErrNoVectorBackend, got %v", err)
	}
}

func TestVectorSimilarStructuredPathStillWorks(t *testing.T) {
	// Sanity check that a `find similar` block WITHOUT `using vector
	// scope` still plans as the existing cosine MLComputation — no
	// regression on the structured-attribute path.
	t.Parallel()
	src := `
find similar "Structured" {
  for records where type == "vehicle"
  to 1
}
`
	plans := compileSrc(t, src)
	plan := plans["Structured"]
	for _, st := range plan.Steps {
		if vs, ok := st.(*planner.VectorSimilarStep); ok {
			t.Fatalf("structured similar block planned a VectorSimilarStep: %+v", vs)
		}
	}
	// Ensure the existing cosine ML step is still emitted.
	found := false
	for _, st := range plan.Steps {
		if ml, ok := st.(*planner.MLComputation); ok && strings.Contains(ml.Function, "similarity") {
			found = true
		}
	}
	if !found {
		t.Errorf("structured similar block missing similarity MLComputation: %+v", plan.Steps)
	}
}

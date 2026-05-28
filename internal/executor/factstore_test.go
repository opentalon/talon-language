package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/opentalon/talon-language/internal/planner"
)

// fakeStore is the test double for FactStore. It records every call
// and replays canned responses, keeping these tests free of the
// JVM-backed Datalevin server.
type fakeStore struct {
	queries    []string
	queryReply func(q string) ([][]any, error)
	txData     []map[string]any
	schemaSeen map[string]map[string]string
}

func (f *fakeStore) Query(_ context.Context, q string) ([][]any, error) {
	f.queries = append(f.queries, q)
	if f.queryReply != nil {
		return f.queryReply(q)
	}
	return nil, nil
}

func (f *fakeStore) Transact(_ context.Context, tx []map[string]any) error {
	f.txData = append(f.txData, tx...)
	return nil
}

func (f *fakeStore) Schema(_ context.Context, s map[string]map[string]string) error {
	f.schemaSeen = s
	return nil
}

// Compile-time guard: fakeStore satisfies FactStore. Catches signature
// drift between the interface and the test double if the executor's
// needs ever evolve.
var _ FactStore = (*fakeStore)(nil)

// TestExecutor_DatalevinQueryHitsFactStore covers the basic path:
// a DatalevinQuery step in a plan calls FactStore.Query with the
// emitted Datalog and the result rows flow into BlockResult.Vars
// + BlockResult.Flagged.
func TestExecutor_DatalevinQueryHitsFactStore(t *testing.T) {
	rows := [][]any{{1}, {2}, {3}}
	fs := &fakeStore{
		queryReply: func(string) ([][]any, error) { return rows, nil },
	}
	e := &Executor{Client: fs}

	plan := &planner.QueryPlan{
		BlockName: "test_detect",
		Steps: []planner.PlanStep{
			&planner.DatalevinQuery{Query: "[:find ?e :where [?e :attr/x ?]]", Into: "matches"},
		},
	}

	result, err := e.Run(context.Background(), plan)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fs.queries) != 1 {
		t.Fatalf("expected 1 Query call, got %d", len(fs.queries))
	}
	if fs.queries[0] != "[:find ?e :where [?e :attr/x ?]]" {
		t.Errorf("query text: %q", fs.queries[0])
	}
	got, ok := result.Vars["matches"].([][]any)
	if !ok {
		t.Fatalf("matches: expected [][]any, got %T", result.Vars["matches"])
	}
	if len(got) != 3 {
		t.Errorf("rows: got %d, want 3", len(got))
	}
	if len(result.Flagged) != 3 {
		t.Errorf("flagged: got %d, want 3", len(result.Flagged))
	}
}

// TestExecutor_FactStoreError surfaces a backend failure as a Run
// error rather than panicking — this is the property a future SQL/
// alternate backend impl must preserve.
func TestExecutor_FactStoreError(t *testing.T) {
	fs := &fakeStore{
		queryReply: func(string) ([][]any, error) { return nil, errors.New("connection refused") },
	}
	e := &Executor{Client: fs}

	plan := &planner.QueryPlan{
		BlockName: "test",
		Steps: []planner.PlanStep{
			&planner.DatalevinQuery{Query: "[:find ?e :where [?e :a ?]]", Into: "x"},
		},
	}
	if _, err := e.Run(context.Background(), plan); err == nil {
		t.Fatal("expected error from FactStore.Query failure")
	}
}

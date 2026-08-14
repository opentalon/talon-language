package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/opentalon/tln-language/internal/factstore"
	"github.com/opentalon/tln-language/internal/planner"
)

// fakeStore is the test double for FactStore. It records every call
// and replays canned responses, keeping these tests free of the
// JVM-backed Datalevin server.
type fakeStore struct {
	queries    []factstore.Query
	queryReply func(q factstore.Query) ([][]any, error)
	asserted   []factstore.Fact
}

func (f *fakeStore) Query(_ context.Context, q factstore.Query) ([][]any, error) {
	f.queries = append(f.queries, q)
	if f.queryReply != nil {
		return f.queryReply(q)
	}
	return nil, nil
}

func (f *fakeStore) Assert(_ context.Context, facts []factstore.Fact) error {
	f.asserted = append(f.asserted, facts...)
	return nil
}

func (f *fakeStore) Retract(_ context.Context, _ factstore.RetractPattern) error {
	return nil
}

// Compile-time guard: fakeStore satisfies FactStore. Catches signature
// drift between the interface and the test double if the executor's
// needs ever evolve.
var _ FactStore = (*fakeStore)(nil)

func sampleQuery() factstore.Query {
	return factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{
				Entity:    factstore.Var("e"),
				Attribute: ":attr/x",
				Value:     factstore.Var("v"),
			},
		},
	}
}

// TestExecutor_FactQueryHitsFactStore covers the basic path:
// a FactQuery step in a plan calls FactStore.Query with the
// structured query and the result rows flow into BlockResult.Vars
// + BlockResult.Flagged.
func TestExecutor_FactQueryHitsFactStore(t *testing.T) {
	rows := [][]any{{1}, {2}, {3}}
	fs := &fakeStore{
		queryReply: func(factstore.Query) ([][]any, error) { return rows, nil },
	}
	e := &Executor{Client: fs}

	plan := &planner.QueryPlan{
		BlockName: "test_detect",
		Steps: []planner.PlanStep{
			&planner.FactQuery{Query: sampleQuery(), Into: "matches"},
		},
	}

	result, err := e.Run(context.Background(), plan)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fs.queries) != 1 {
		t.Fatalf("expected 1 Query call, got %d", len(fs.queries))
	}
	if got, want := len(fs.queries[0].Find), 1; got != want {
		t.Errorf("Find columns: got %d, want %d", got, want)
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
		queryReply: func(factstore.Query) ([][]any, error) { return nil, errors.New("connection refused") },
	}
	e := &Executor{Client: fs}

	plan := &planner.QueryPlan{
		BlockName: "test",
		Steps: []planner.PlanStep{
			&planner.FactQuery{Query: sampleQuery(), Into: "x"},
		},
	}
	if _, err := e.Run(context.Background(), plan); err == nil {
		t.Fatal("expected error from FactStore.Query failure")
	}
}

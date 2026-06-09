package talon_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/opentalon/talon-language/internal/factstore"
	"github.com/opentalon/talon-language/pkg/talon"
)

// fakeStore is an in-memory FactStore used to exercise Run / Seed
// without spinning up the JVM Datalevin server. It records every
// call and lets each test shape the Query reply via the optional
// reply closure.
type fakeStore struct {
	mu         sync.Mutex
	queries    []factstore.Query
	queryReply func(q factstore.Query) ([][]any, error)
	asserted   []factstore.Fact
}

func (f *fakeStore) Query(_ context.Context, q factstore.Query) ([][]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	if f.queryReply != nil {
		return f.queryReply(q)
	}
	return nil, nil
}

func (f *fakeStore) Assert(_ context.Context, facts []factstore.Fact) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asserted = append(f.asserted, facts...)
	return nil
}

func (f *fakeStore) Retract(_ context.Context, _ factstore.RetractPattern) error {
	return nil
}

// Compile-time guard: fakeStore satisfies the public FactStore type
// aliased from the canonical factstore interface.
var _ talon.FactStore = (*fakeStore)(nil)

// queryMentions reports whether any clause in q targets the given attr.
// Used in tests as a structured analogue of the old `strings.Contains`
// checks against the wire-format Datalog.
func queryMentions(q factstore.Query, attr string) bool {
	for _, c := range q.Where {
		if p, ok := c.(*factstore.Pattern); ok && p.Attribute == attr {
			return true
		}
	}
	return false
}

// A minimal detect program — enough to force a FactQuery into
// the plan so Run actually hits the FactStore. Conditions are kept
// in line (no define references) so the test source is self-contained.
const detectSrc = `
detect "Low stock" {
  for records where type == "stock_item"
    and attr "current_stock" <= attr "minimum_amount"
  flag matching items
  label "{item.name}: low stock"
  priority HIGH
}
`

func TestRun_DetectBlockHitsFactStore(t *testing.T) {
	rows := [][]any{{1}, {2}, {3}}
	fs := &fakeStore{
		queryReply: func(q factstore.Query) ([][]any, error) {
			// First call is the detect query, second (if any) is
			// ResolveNames. Both return canned rows; ResolveNames'
			// query is identifiable by its single :attr/name pattern.
			if queryMentions(q, ":attr/name") {
				return [][]any{{1, "Truck A"}, {2, "Truck B"}}, nil
			}
			return rows, nil
		},
	}
	result, err := talon.Run(context.Background(), detectSrc, talon.WithFactStore(fs))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fs.queries) == 0 {
		t.Fatal("expected at least one Query call")
	}
	// Detect block should produce at least one BlockResult.
	if len(result.Blocks) == 0 {
		t.Fatalf("expected block results, got none")
	}
	// ResolveNames is best-effort; if it was called, the names map
	// should be populated.
	if result.ResolvedNames != nil && result.ResolvedNames[1] != "Truck A" {
		t.Errorf("ResolvedNames[1] = %q, want Truck A", result.ResolvedNames[1])
	}
}

func TestRun_MissingFactStore_ErrRequiresFactStore(t *testing.T) {
	_, err := talon.Run(context.Background(), detectSrc)
	if !errors.Is(err, talon.ErrRequiresFactStore) {
		t.Fatalf("err = %v, want ErrRequiresFactStore", err)
	}
}

func TestRunWorkflow_RejectsDetectBlocks(t *testing.T) {
	_, err := talon.RunWorkflow(context.Background(), detectSrc)
	if !errors.Is(err, talon.ErrRequiresFactStore) {
		t.Fatalf("err = %v, want ErrRequiresFactStore", err)
	}
}

func TestRunWorkflow_StillWorksForWorkflowOnly(t *testing.T) {
	// Regression: the new gate in RunWorkflow must not break
	// existing workflow-only callers (i.e. talon-plugin's
	// execute_workflow path).
	src := `
workflow "ok" {
  step "s1" {
    mcp "srv" "tool" {
      x 1
    }
  }
}`
	result, err := talon.RunWorkflow(context.Background(), src)
	if err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}
	if _, ok := result.Blocks["ok"]; !ok {
		t.Errorf("missing block result; got %v", result.Blocks)
	}
}

func TestSeed_RoundTrip(t *testing.T) {
	src := `
test "seed" {
  given {
    record 501 type "item" status "active"
    attr 501 "name" "Truck A"
    attr 501 "km" 45000
  }
  when detect "Service overdue"
  expect {
    flagged 501
  }
}`
	fs := &fakeStore{}
	n, err := talon.Seed(context.Background(), fs, src)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if n != 1 {
		t.Errorf("entities seeded: %d, want 1", n)
	}
	if len(fs.asserted) == 0 {
		t.Error("Seed should have asserted at least one fact")
	}
}

func TestSeed_NilStore(t *testing.T) {
	_, err := talon.Seed(context.Background(), nil, `test "x" { given {} when detect "y" expect {} }`)
	if err == nil {
		t.Fatal("expected error when store is nil")
	}
	if !strings.Contains(err.Error(), "FactStore") {
		t.Errorf("error should mention FactStore: %v", err)
	}
}

func TestRun_WithDatalevinURL_FailsFastOnUnreachable(t *testing.T) {
	// The URL sugar runs Health() on first store access; an
	// unreachable URL should surface as a clean Run error, not a
	// panic deep in the executor.
	_, err := talon.Run(context.Background(), detectSrc,
		talon.WithDatalevinURL("http://127.0.0.1:1"), // closed port
	)
	if err == nil {
		t.Fatal("expected error from unreachable datalevin URL")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Errorf("error should reference the URL: %v", err)
	}
}

func TestNewFactStore_FailsAtFirstUse(t *testing.T) {
	// Construction returns a usable handle even with an unreachable
	// URL — the health check fires lazily on first Query/Transact/
	// Schema. That contract lets callers (e.g. talon-plugin) build
	// the store at startup without blocking on the backend being
	// up; the error surfaces the moment any operation needs it.
	fs := talon.NewFactStore("http://127.0.0.1:1")
	if fs == nil {
		t.Fatal("NewFactStore returned nil")
	}
	q := factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":x", Value: factstore.Var("v")},
		},
	}
	_, err := fs.Query(context.Background(), q)
	if err == nil {
		t.Fatal("expected error for unreachable URL")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Errorf("error should reference URL: %v", err)
	}
}

// Compile-time surface guards — any rename in the public API breaks
// the test build instead of silently slipping through.
var (
	_ = talon.Run
	_ = talon.Seed
	_ = talon.WithFactStore
	_ = talon.WithDatalevinURL
	_ = talon.NewFactStore
	_ = talon.ErrRequiresFactStore
)

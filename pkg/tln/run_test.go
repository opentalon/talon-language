package tln_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opentalon/tln-language/internal/factstore"
	"github.com/opentalon/tln-language/pkg/tln"
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
var _ tln.FactStore = (*fakeStore)(nil)

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
	result, err := tln.Run(context.Background(), detectSrc, tln.WithFactStore(fs))
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
	_, err := tln.Run(context.Background(), detectSrc)
	if !errors.Is(err, tln.ErrRequiresFactStore) {
		t.Fatalf("err = %v, want ErrRequiresFactStore", err)
	}
}

func TestRunWorkflow_RejectsDetectBlocks(t *testing.T) {
	_, err := tln.RunWorkflow(context.Background(), detectSrc)
	if !errors.Is(err, tln.ErrRequiresFactStore) {
		t.Fatalf("err = %v, want ErrRequiresFactStore", err)
	}
}

func TestRunWorkflow_StillWorksForWorkflowOnly(t *testing.T) {
	// Regression: the new gate in RunWorkflow must not break
	// existing workflow-only callers (i.e. tln-plugin's
	// execute_workflow path).
	src := `
workflow "ok" {
  step "s1" {
    tool "srv" "tool" {
      x 1
    }
  }
}`
	result, err := tln.RunWorkflow(context.Background(), src)
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
	n, err := tln.Seed(context.Background(), fs, src)
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
	_, err := tln.Seed(context.Background(), nil, `test "x" { given {} when detect "y" expect {} }`)
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
	_, err := tln.Run(context.Background(), detectSrc,
		tln.WithDatalevinURL("http://127.0.0.1:1"), // closed port
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
	// Schema. That contract lets callers (e.g. tln-plugin) build
	// the store at startup without blocking on the backend being
	// up; the error surfaces the moment any operation needs it.
	fs := tln.NewFactStore("http://127.0.0.1:1")
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
	_ = tln.Run
	_ = tln.Seed
	_ = tln.WithFactStore
	_ = tln.WithDatalevinURL
	_ = tln.NewFactStore
	_ = tln.ErrRequiresFactStore
)

// ─── remediate (issue #53) ─────────────────────────────────────────────────

const remediateSrc = `
detect "Defective without ticket" {
  for records where status == "defective"
  flag matching items
  remediate {
    tool "inventory" "create-ticket" {
      title "Auto: {item.name} is defective"
      item_id attr "id"
      priority "high"
    }
  }
}`

func seedDefectiveItems(t *testing.T) *factstore.MemoryStore {
	t.Helper()
	store := tln.NewMemoryStore()
	facts := []tln.Fact{
		{RecordID: "1", Attribute: ":record/status", Value: "defective"},
		{RecordID: "1", Attribute: ":attr/name", Value: "Broken Drill"},
		{RecordID: "2", Attribute: ":record/status", Value: "defective"},
		{RecordID: "2", Attribute: ":attr/name", Value: "Cracked Saw"},
		{RecordID: "3", Attribute: ":record/status", Value: "ok"},
		{RecordID: "3", Attribute: ":attr/name", Value: "Good Hammer"},
	}
	if err := store.Assert(context.Background(), facts); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return store
}

func TestRun_RemediateFiresPerFlaggedRow(t *testing.T) {
	store := seedDefectiveItems(t)
	caller := &mockCaller{}
	_, err := tln.Run(context.Background(), remediateSrc,
		tln.WithFactStore(store), tln.WithToolResolver(caller))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Two defective items flagged → one create-ticket call each; the "ok"
	// item is not flagged and triggers nothing.
	if len(caller.calls) != 2 {
		t.Fatalf("expected 2 remediate calls, got %d: %+v", len(caller.calls), caller.calls)
	}
	titleByItem := map[int]any{}
	for _, c := range caller.calls {
		if c.Server != "inventory" || c.Tool != "create-ticket" {
			t.Errorf("unexpected call target %s/%s", c.Server, c.Tool)
		}
		if c.Args["priority"] != "high" {
			t.Errorf("priority arg: got %v", c.Args["priority"])
		}
		id, _ := c.Args["item_id"].(int)
		titleByItem[id] = c.Args["title"]
	}
	if titleByItem[1] != "Auto: Broken Drill is defective" {
		t.Errorf("row 1 title: got %v", titleByItem[1])
	}
	if titleByItem[2] != "Auto: Cracked Saw is defective" {
		t.Errorf("row 2 title: got %v", titleByItem[2])
	}
}

func TestRun_RemediateNoCallerNoDispatch(t *testing.T) {
	store := seedDefectiveItems(t)
	// No WithToolResolver: remediate must be a no-op, not an error.
	if _, err := tln.Run(context.Background(), remediateSrc, tln.WithFactStore(store)); err != nil {
		t.Fatalf("Run without MCP caller should not error: %v", err)
	}
}

// ─── enrich (issue #54) ────────────────────────────────────────────────────

const enrichSrc = `
enrich "Refresh stock" {
  for records where type == "stock_item"
  stale_after 1 hour
  tool "inventory" "show-item" {
    id attr "id"
  }
  update attr "current_stock" from result.current_stock
}`

func TestRun_EnrichRefreshesStaleFacts(t *testing.T) {
	store := tln.NewMemoryStore()
	ctx := context.Background()

	// Seed current_stock as written 2h ago — stale for a 1h window.
	store.SetClock(func() time.Time { return time.Now().Add(-2 * time.Hour) })
	if err := store.Assert(ctx, []tln.Fact{
		{RecordID: "1", Attribute: ":record/type", Value: "stock_item"},
		{RecordID: "1", Attribute: ":attr/current_stock", Value: 3.0},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	store.SetClock(time.Now)

	caller := &mockCaller{handler: func(_, _ string, args map[string]any) (any, error) {
		if args["id"] != 1 {
			t.Errorf("enrich mcp id arg = %v, want 1", args["id"])
		}
		return map[string]any{"current_stock": 42.0}, nil
	}}
	if _, err := tln.Run(ctx, enrichSrc, tln.WithFactStore(store), tln.WithToolResolver(caller)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(caller.calls) != 1 {
		t.Fatalf("stale fact should trigger one enrich call, got %d", len(caller.calls))
	}
	// The response field was asserted back onto the record.
	if got := store.Snapshot()[1][":attr/current_stock"]; got != 42.0 {
		t.Errorf("current_stock after enrich = %v, want 42", got)
	}
}

func TestRun_EnrichSkipsFreshFacts(t *testing.T) {
	store := tln.NewMemoryStore()
	ctx := context.Background()
	// Written just now — fresh for a 1h window.
	if err := store.Assert(ctx, []tln.Fact{
		{RecordID: "1", Attribute: ":record/type", Value: "stock_item"},
		{RecordID: "1", Attribute: ":attr/current_stock", Value: 3.0},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	caller := &mockCaller{}
	if _, err := tln.Run(ctx, enrichSrc, tln.WithFactStore(store), tln.WithToolResolver(caller)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(caller.calls) != 0 {
		t.Fatalf("fresh fact should not trigger enrich, got %d calls", len(caller.calls))
	}
}

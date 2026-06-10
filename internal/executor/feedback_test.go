package executor

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/opentalon/talon-language/internal/factstore"
)

// seedFeedback writes N feedback facts of the given outcome for a
// block, timestamped at `at` (unix seconds).
func seedFeedback(t *testing.T, store *factstore.MemoryStore, block, outcome string, n int, at float64) {
	t.Helper()
	facts := []factstore.Fact{}
	for i := 0; i < n; i++ {
		// Negative IDs so we don't collide with user records.
		recID := strconv.Itoa(-100000 - int(at)*100 - i)
		facts = append(facts,
			factstore.Fact{RecordID: recID, Attribute: ":record/type", Value: "feedback"},
			factstore.Fact{RecordID: recID, Attribute: ":feedback/block", Value: block},
			factstore.Fact{RecordID: recID, Attribute: ":feedback/outcome", Value: outcome},
			factstore.Fact{RecordID: recID, Attribute: ":feedback/at", Value: at},
		)
	}
	if err := store.Assert(context.Background(), facts); err != nil {
		t.Fatalf("seed feedback %d %s: %v", n, outcome, err)
	}
}

func TestFeedback_PosteriorShiftsTowardAcceptedRate(t *testing.T) {
	// Prior 0.5. Window: 30 days. Seed 8 accepts and 2 rejects
	// inside the window. Posterior should move toward 0.8.
	store := factstore.NewMemoryStore()
	ctx := context.Background()
	now := float64(time.Now().Unix())
	seedFeedback(t, store, "Block A", "accept", 8, now-1) // 1 second ago
	seedFeedback(t, store, "Block A", "reject", 2, now-2)

	ex := NewExecutor(store)
	post, err := ex.adjustWithFeedback(ctx, "Block A", 0.5, 30)
	if err != nil {
		t.Fatalf("adjust: %v", err)
	}
	// posterior mean = (0.5 + 8) / (1 + 8 + 2) = 8.5 / 11 ≈ 0.7727
	if post < 0.75 || post > 0.80 {
		t.Errorf("posterior = %v, want ~0.77", post)
	}
}

func TestFeedback_NoFeedbackKeepsPrior(t *testing.T) {
	store := factstore.NewMemoryStore()
	ex := NewExecutor(store)
	post, err := ex.adjustWithFeedback(context.Background(), "Block X", 0.3, 7)
	if err != nil {
		t.Fatalf("adjust: %v", err)
	}
	// Zero feedback → posterior == prior.
	if post != 0.3 {
		t.Errorf("posterior with no feedback = %v, want 0.3 (prior unchanged)", post)
	}
}

func TestFeedback_StaleEventsIgnored(t *testing.T) {
	// 10 rejects outside the 7-day window should not pull the
	// posterior down.
	store := factstore.NewMemoryStore()
	ctx := context.Background()
	now := float64(time.Now().Unix())
	stale := now - 30*86400 // 30 days ago
	seedFeedback(t, store, "Block S", "reject", 10, stale)

	ex := NewExecutor(store)
	post, err := ex.adjustWithFeedback(ctx, "Block S", 0.7, 7)
	if err != nil {
		t.Fatalf("adjust: %v", err)
	}
	if post != 0.7 {
		t.Errorf("posterior = %v, want 0.7 (stale rejects should be ignored)", post)
	}
}

func TestFeedback_ClampsExtremeValues(t *testing.T) {
	store := factstore.NewMemoryStore()
	ctx := context.Background()
	now := float64(time.Now().Unix())
	// Unanimous accepts — without the clamp, posterior would
	// approach 1.0 and lock exploration off.
	seedFeedback(t, store, "Block C", "accept", 100, now-1)

	ex := NewExecutor(store)
	post, err := ex.adjustWithFeedback(ctx, "Block C", 0.5, 30)
	if err != nil {
		t.Fatalf("adjust: %v", err)
	}
	if post > 0.99 {
		t.Errorf("posterior = %v, want ≤ 0.99 (clamp)", post)
	}
}

func TestFeedback_RecommendEndToEnd(t *testing.T) {
	// Parse a recommend block with `learn from feedback within 30
	// days`, seed accept-heavy feedback, run, assert the
	// effective probability shifted upward and trace IDs were
	// minted.
	//
	// The recommend block needs a `when detect "X"` so the
	// planner produces a working chain; we synthesise a detect
	// upstream that flags every order.
	src := `
detect "Low stock" {
  for records where type == "order"
  flag matching items
  label "{item.name}"
  priority HIGH
}

recommend "Restock now" {
  when detect "Low stock"
  suggest "Order more of {item.name}" with probability 0.5 learn from feedback within 30 days
  priority HIGH
}
`
	plans := compileSrc(t, src)
	store := factstore.NewMemoryStore()
	ctx := context.Background()

	// Seed orders so the detect fires.
	for i := 1; i <= 4; i++ {
		seedEntity(t, store, i, map[string]any{
			"__type": "order",
			"name":   "Widget " + strconv.Itoa(i),
		})
	}
	// 8 accepts, 2 rejects within window → posterior ≈ 0.77.
	now := float64(time.Now().Unix())
	seedFeedback(t, store, "Restock now", "accept", 8, now-1)
	seedFeedback(t, store, "Restock now", "reject", 2, now-2)

	ex := NewExecutor(store)
	ex.RandSeed = 42 // deterministic gating for the test
	blocks, err := ex.RunAll(ctx, plans)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	res := blocks["Restock now"]
	if res == nil {
		t.Fatal("no result for Restock now")
	}
	sugg, ok := res.Vars["suggestions"].(map[string]any)
	if !ok {
		t.Fatalf("suggestions = %T", res.Vars["suggestions"])
	}
	effective, _ := sugg["probability"].(float64)
	prior, _ := sugg["prior_probability"].(float64)
	if prior != 0.5 {
		t.Errorf("prior_probability = %v, want 0.5", prior)
	}
	if effective < 0.75 || effective > 0.80 {
		t.Errorf("effective probability = %v, want ~0.77 after feedback adjustment", effective)
	}

	// Trace IDs should be minted for every kept row.
	if ids, ok := sugg["trace_ids"].([]string); ok {
		if len(ids) == 0 {
			t.Error("no trace IDs minted despite kept rows")
		}
	}
}

func TestFeedback_MintTraceIDs(t *testing.T) {
	store := factstore.NewMemoryStore()
	ctx := context.Background()
	rows := [][]any{
		{int64(501)},
		{int64(502)},
	}

	ex := NewExecutor(store)
	ids, err := ex.mintTraces(ctx, "Block T", rows)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("ids = %v, want 2", ids)
	}
	if ids[0] == ids[1] {
		t.Errorf("trace IDs collide: %v", ids)
	}

	// Verify traces are queryable: every fired trace shows up as
	// a record with :suggest/block bound to this block. One row
	// per minted trace.
	q := factstore.Query{
		Find: []string{"?t"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("t"), Attribute: ":suggest/block", Value: factstore.Lit("Block T")},
		},
	}
	out, err := store.Query(ctx, q)
	if err != nil {
		t.Fatalf("query traces: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("queried %d traces, want 2: %v", len(out), out)
	}
}

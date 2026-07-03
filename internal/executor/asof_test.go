package executor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/factstore"
	"github.com/opentalon/talon-language/internal/planner"
)

// asOfPlan mirrors what the planner emits for
//
//	for records where type == "machine" and was (attr "status" == "certified") 90 days ago
func asOfPlan() *planner.QueryPlan {
	delta := ast.Duration{Value: 90, Unit: "days"}
	return &planner.QueryPlan{
		BlockName: "regressed",
		Steps: []planner.PlanStep{
			&planner.FactQuery{
				Into: "candidates",
				Query: factstore.Query{
					Find:  []string{"?e"},
					Where: []factstore.Clause{&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Lit("machine")}},
				},
			},
			&planner.FactQuery{
				Into:      "asof_0",
				AsOfDelta: &delta,
				Query: factstore.Query{
					Find:  []string{"?e"},
					Where: []factstore.Clause{&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/status", Value: factstore.Lit("certified")}},
				},
			},
			&planner.GoComputation{
				Function: planner.FuncAsOfIntersect,
				Input:    "candidates",
				Params:   map[string]any{"with": "asof_0"},
				Into:     "asof_intersected_0",
			},
		},
	}
}

// TestExecutorWasAgoEndToEnd runs the full plan against a history-backed
// MemoryStore. A machine that was certified 90 days ago but is defective
// now is flagged; one that was never certified is not.
func TestExecutorWasAgoEndToEnd(t *testing.T) {
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	store := factstore.NewMemoryStore()

	// t0 = 120 days ago: r1 certified, r3 defective (both machines).
	store.SetClock(func() time.Time { return now.AddDate(0, 0, -120) })
	must(t, store.Assert(context.Background(), []factstore.Fact{
		{RecordID: "1", Attribute: ":record/type", Value: "machine"},
		{RecordID: "1", Attribute: ":record/status", Value: "certified"},
		{RecordID: "3", Attribute: ":record/type", Value: "machine"},
		{RecordID: "3", Attribute: ":record/status", Value: "defective"},
	}))

	// t1 = 10 days ago: r1 regresses to defective.
	store.SetClock(func() time.Time { return now.AddDate(0, 0, -10) })
	must(t, store.Assert(context.Background(), []factstore.Fact{
		{RecordID: "1", Attribute: ":record/status", Value: "defective"},
	}))

	e := &Executor{Client: store, Now: func() time.Time { return now }}
	res, err := e.Run(context.Background(), asOfPlan())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	ids := flaggedIDs(res.Flagged)
	if len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("flagged = %v, want [1] (r1 regressed; r3 never certified)", ids)
	}
}

// TestExecutorWasAgoNoTimeTravel surfaces ErrNoTimeTravel when the backend
// lacks the capability.
func TestExecutorWasAgoNoTimeTravel(t *testing.T) {
	e := &Executor{Client: &fakeStore{
		queryReply: func(factstore.Query) ([][]any, error) { return [][]any{{1.0}}, nil },
	}}
	_, err := e.Run(context.Background(), asOfPlan())
	if !errors.Is(err, factstore.ErrNoTimeTravel) {
		t.Fatalf("err = %v, want ErrNoTimeTravel", err)
	}
}

func flaggedIDs(rows [][]any) []int {
	out := make([]int, 0, len(rows))
	for _, r := range rows {
		if len(r) > 0 {
			if f, ok := r[0].(float64); ok {
				out = append(out, int(f))
			}
		}
	}
	return out
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
}

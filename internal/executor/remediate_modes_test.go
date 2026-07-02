package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/factstore"
	"github.com/opentalon/talon-language/internal/planner"
)

type stubQueue struct{ calls []QueuedCall }

func (q *stubQueue) Enqueue(_ context.Context, _ string, c QueuedCall) error {
	q.calls = append(q.calls, c)
	return nil
}

// runRemediateMode drives execRemediate for one flagged entity (id 501)
// with the given mode, returning the executor's mock so callers can
// inspect the MCP calls made.
func runRemediateMode(t *testing.T, e *Executor, mode, role, batch string) {
	t.Helper()
	if e.Client == nil {
		e.Client = factstore.NewMemoryStore()
	}
	call := &ast.MCPCall{Server: "inv", Tool: "do", Args: map[string]ast.Expr{
		"item_id": &ast.AttrExpr{Name: "id"},
	}}
	gc := &planner.GoComputation{
		Function: planner.FuncRemediateMCP,
		Input:    "candidates",
		Params: map[string]any{
			"calls": []*ast.MCPCall{call}, "block_name": "blk",
			"mode": mode, "role": role, "batch": batch,
		},
	}
	vars := map[string]any{"candidates": [][]any{{float64(501)}}}
	if _, err := e.execRemediate(context.Background(), gc, vars); err != nil {
		t.Fatalf("execRemediate(%s): %v", mode, err)
	}
}

func TestRemediateAutoFiresDirectly(t *testing.T) {
	mock := &mockMCP{}
	e := &Executor{MCP: mock}
	runRemediateMode(t, e, "auto", "", "")
	if len(mock.calls) != 1 || mock.calls[0].Args["item_id"] != 501 {
		t.Fatalf("auto should call MCP once with item_id 501, got %+v", mock.calls)
	}
}

func TestRemediateProposeRespectsConfirmHook(t *testing.T) {
	// Deny → no call.
	mock := &mockMCP{}
	e := &Executor{MCP: mock, ConfirmHook: func(context.Context, string, string, string) (bool, error) { return false, nil }}
	runRemediateMode(t, e, "propose", "", "")
	if len(mock.calls) != 0 {
		t.Errorf("propose denied should not call, got %d", len(mock.calls))
	}
	// Allow → call.
	mock2 := &mockMCP{}
	e2 := &Executor{MCP: mock2, ConfirmHook: func(context.Context, string, string, string) (bool, error) { return true, nil }}
	runRemediateMode(t, e2, "propose", "", "")
	if len(mock2.calls) != 1 {
		t.Errorf("propose allowed should call once, got %d", len(mock2.calls))
	}
}

func TestRemediateApprove(t *testing.T) {
	// Granted.
	mock := &mockMCP{}
	var gotRole, gotRule string
	e := &Executor{MCP: mock, ApprovalHook: func(_ context.Context, role, rule string, _ map[string]any) (bool, error) {
		gotRole, gotRule = role, rule
		return true, nil
	}}
	runRemediateMode(t, e, "approve", "manager", "")
	if len(mock.calls) != 1 {
		t.Errorf("approved should call once, got %d", len(mock.calls))
	}
	if gotRole != "manager" || gotRule != "blk" {
		t.Errorf("approval hook got role=%q rule=%q", gotRole, gotRule)
	}

	// Denied.
	mock2 := &mockMCP{}
	e2 := &Executor{MCP: mock2, ApprovalHook: func(context.Context, string, string, map[string]any) (bool, error) { return false, nil }}
	runRemediateMode(t, e2, "approve", "manager", "")
	if len(mock2.calls) != 0 {
		t.Errorf("denied should not call, got %d", len(mock2.calls))
	}

	// No approver wired → skipped.
	mock3 := &mockMCP{}
	e3 := &Executor{MCP: mock3}
	runRemediateMode(t, e3, "approve", "manager", "")
	if len(mock3.calls) != 0 {
		t.Errorf("no approver should skip, got %d calls", len(mock3.calls))
	}
}

func TestRemediateQueueDefersCall(t *testing.T) {
	mock := &mockMCP{}
	q := &stubQueue{}
	e := &Executor{MCP: mock, Queue: q}
	runRemediateMode(t, e, "queue", "", "weekly-cleanup")
	if len(mock.calls) != 0 {
		t.Errorf("queue must not call MCP, got %d", len(mock.calls))
	}
	if len(q.calls) != 1 || q.calls[0].Tool != "do" || q.calls[0].Args["item_id"] != 501 {
		t.Fatalf("queue should receive one call, got %+v", q.calls)
	}
}

func TestRemediateApproveHookError(t *testing.T) {
	e := &Executor{MCP: &mockMCP{}, ApprovalHook: func(context.Context, string, string, map[string]any) (bool, error) {
		return false, errors.New("approver down")
	}}
	call := &ast.MCPCall{Server: "inv", Tool: "do"}
	gc := &planner.GoComputation{Function: planner.FuncRemediateMCP, Input: "candidates", Params: map[string]any{
		"calls": []*ast.MCPCall{call}, "mode": "approve", "block_name": "blk",
	}}
	e.Client = factstore.NewMemoryStore()
	if _, err := e.execRemediate(context.Background(), gc, map[string]any{"candidates": [][]any{{float64(1)}}}); err == nil {
		t.Error("approval hook error should propagate")
	}
}

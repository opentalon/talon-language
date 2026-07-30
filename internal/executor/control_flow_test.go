package executor

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/factstore"
	"github.com/opentalon/talon-language/internal/planner"
)

// runRemediateBody drives execRemediate (auto mode) over one flagged entity
// with the given attrs and action body, returning the recording mock.
func runRemediateBody(t *testing.T, id int, attrs map[string]any, body []ast.Action) *mockMCP {
	t.Helper()
	store := factstore.NewMemoryStore()
	facts := make([]factstore.Fact, 0, len(attrs))
	rid := strconv.Itoa(id)
	for k, v := range attrs {
		facts = append(facts, factstore.Fact{RecordID: rid, Attribute: ":attr/" + k, Value: v})
	}
	must(t, store.Assert(context.Background(), facts))

	mock := &mockMCP{}
	e := &Executor{Client: store, MCP: mock}
	gc := &planner.GoComputation{
		Function: planner.FuncRemediateMCP,
		Input:    "candidates",
		Params:   map[string]any{"body": body, "mode": "auto", "block_name": "blk"},
	}
	vars := map[string]any{"candidates": [][]any{{float64(id)}}}
	if _, err := e.execRemediate(context.Background(), gc, vars); err != nil {
		t.Fatalf("execRemediate: %v", err)
	}
	return mock
}

func mcpAction(server, tool string, args map[string]ast.Expr) *ast.MCPAction {
	return &ast.MCPAction{Call: &ast.MCPCall{Server: server, Tool: tool, Args: args}}
}

// TestIfActionTakesThenBranch: a true guard fires the `then` body only.
func TestIfActionTakesThenBranch(t *testing.T) {
	body := []ast.Action{
		&ast.IfAction{
			Cond: &ast.CompareCondition{
				Left:  &ast.AttrExpr{Name: "priority"},
				Op:    "==",
				Right: &ast.LiteralExpr{Value: "CRITICAL"},
			},
			Then: []ast.Action{mcpAction("ops", "page", nil)},
			Else: []ast.Action{mcpAction("ops", "ticket", nil)},
		},
	}
	mock := runRemediateBody(t, 1, map[string]any{"priority": "CRITICAL"}, body)
	if len(mock.calls) != 1 || mock.calls[0].Tool != "page" {
		t.Fatalf("expected only page to fire, got %+v", mock.calls)
	}
}

// TestIfActionTakesElseBranch: a false guard fires the `else` body only.
func TestIfActionTakesElseBranch(t *testing.T) {
	body := []ast.Action{
		&ast.IfAction{
			Cond: &ast.CompareCondition{
				Left:  &ast.AttrExpr{Name: "priority"},
				Op:    "==",
				Right: &ast.LiteralExpr{Value: "CRITICAL"},
			},
			Then: []ast.Action{mcpAction("ops", "page", nil)},
			Else: []ast.Action{mcpAction("ops", "ticket", nil)},
		},
	}
	mock := runRemediateBody(t, 1, map[string]any{"priority": "LOW"}, body)
	if len(mock.calls) != 1 || mock.calls[0].Tool != "ticket" {
		t.Fatalf("expected only ticket to fire, got %+v", mock.calls)
	}
}

// TestForEachIteratesListLiteral: the body fires once per list element, with
// the loop variable bound as an MCP arg value each pass.
func TestForEachIteratesListLiteral(t *testing.T) {
	body := []ast.Action{
		&ast.ForEachAction{
			Variable: "channel",
			Over: &ast.ListExpr{Elements: []ast.Expr{
				&ast.LiteralExpr{Value: "fleet-ops"},
				&ast.LiteralExpr{Value: "maintenance"},
			}},
			Body: []ast.Action{
				mcpAction("slack", "notify", map[string]ast.Expr{"channel": &ast.IdentExpr{Name: "channel"}}),
			},
		},
	}
	mock := runRemediateBody(t, 1, nil, body)
	if len(mock.calls) != 2 {
		t.Fatalf("expected 2 notify calls, got %d: %+v", len(mock.calls), mock.calls)
	}
	got := []any{mock.calls[0].Args["channel"], mock.calls[1].Args["channel"]}
	if got[0] != "fleet-ops" || got[1] != "maintenance" {
		t.Fatalf("loop variable not bound per element: %+v", got)
	}
}

// TestWhileZeroIterationsWhenGuardFalse: a guard that is false on entry runs
// the body zero times (and never errors).
func TestWhileZeroIterationsWhenGuardFalse(t *testing.T) {
	body := []ast.Action{
		&ast.WhileAction{
			Cond: &ast.CompareCondition{
				Left:  &ast.AttrExpr{Name: "open"},
				Op:    "==",
				Right: &ast.LiteralExpr{Value: float64(1)},
			},
			Body:    []ast.Action{mcpAction("ops", "retry", nil)},
			MaxIter: ast.DefaultWhileMaxIter,
		},
	}
	mock := runRemediateBody(t, 1, map[string]any{"open": float64(0)}, body)
	if len(mock.calls) != 0 {
		t.Fatalf("expected no calls when guard false, got %+v", mock.calls)
	}
}

// TestWhileHitsIterationCap: with no mutable loop state a perpetually-true
// guard must error at the cap rather than spin forever.
func TestWhileHitsIterationCap(t *testing.T) {
	store := factstore.NewMemoryStore()
	must(t, store.Assert(context.Background(), []factstore.Fact{
		{RecordID: "1", Attribute: ":attr/open", Value: float64(1)},
	}))
	mock := &mockMCP{}
	e := &Executor{Client: store, MCP: mock}
	body := []ast.Action{
		&ast.WhileAction{
			Cond: &ast.CompareCondition{
				Left:  &ast.AttrExpr{Name: "open"},
				Op:    "==",
				Right: &ast.LiteralExpr{Value: float64(1)},
			},
			Body:    []ast.Action{mcpAction("ops", "retry", nil)},
			MaxIter: 5,
		},
	}
	gc := &planner.GoComputation{
		Function: planner.FuncRemediateMCP,
		Input:    "candidates",
		Params:   map[string]any{"body": body, "mode": "auto", "block_name": "blk"},
	}
	vars := map[string]any{"candidates": [][]any{{float64(1)}}}
	_, err := e.execRemediate(context.Background(), gc, vars)
	if err == nil || !strings.Contains(err.Error(), "exceeded 5 iterations") {
		t.Fatalf("expected iteration-cap error, got %v", err)
	}
	if len(mock.calls) != 5 {
		t.Fatalf("expected exactly MaxIter (5) calls before the cap, got %d", len(mock.calls))
	}
}

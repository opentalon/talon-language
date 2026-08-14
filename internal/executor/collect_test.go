package executor

import (
	"context"
	"testing"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/factstore"
)

func TestRunCollect_FetchesAndAsserts(t *testing.T) {
	store := factstore.NewMemoryStore()
	mock := &mockMCP{handler: func(_, _ string, args map[string]any) (any, error) {
		if args["query"] != "status:defective" {
			t.Errorf("query arg: got %v", args["query"])
		}
		return map[string]any{"items": []any{
			map[string]any{"id": float64(501), "name": "Broken Drill", "status": "defective"},
			map[string]any{"id": float64(502), "name": "Cracked Saw", "status": "defective"},
		}}, nil
	}}
	e := &Executor{MCP: mock, Client: store}

	block := &ast.CollectBlock{
		Name:     "Failure training data",
		Schedule: "weekly",
		Call: &ast.MCPCall{Server: "inventory", Tool: "list-items", Args: map[string]ast.Expr{
			"query": &ast.LiteralExpr{Value: "status:defective"},
		}},
		StoreAs: "training_facts",
		Tag:     "failure_training",
	}

	n, err := e.RunCollect(context.Background(), block)
	if err != nil {
		t.Fatalf("RunCollect: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 records, got %d", n)
	}
	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 mcp call, got %d", len(mock.calls))
	}

	snap := store.Snapshot()
	rec := snap[501]
	if rec == nil {
		t.Fatalf("record 501 not asserted; snapshot: %v", snap)
	}
	if rec[":record/type"] != "training_facts" {
		t.Errorf("type: got %v", rec[":record/type"])
	}
	if rec[":attr/tag"] != "failure_training" {
		t.Errorf("tag: got %v", rec[":attr/tag"])
	}
	if rec[":attr/name"] != "Broken Drill" {
		t.Errorf("name: got %v", rec[":attr/name"])
	}
	if snap[502] == nil || snap[502][":attr/name"] != "Cracked Saw" {
		t.Errorf("record 502: %v", snap[502])
	}
}

func TestRunCollect_NoToolResolverIsNoOp(t *testing.T) {
	store := factstore.NewMemoryStore()
	e := &Executor{Client: store} // no MCP
	block := &ast.CollectBlock{
		Name: "x", Schedule: "daily",
		Call:    &ast.MCPCall{Server: "s", Tool: "t"},
		StoreAs: "facts",
	}
	n, err := e.RunCollect(context.Background(), block)
	if err != nil || n != 0 {
		t.Fatalf("no-MCP collect should be a no-op: n=%d err=%v", n, err)
	}
	if store.Len() != 0 {
		t.Errorf("no facts should be asserted, got %d", store.Len())
	}
}

package executor

import (
	"context"
	"fmt"
	"testing"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/planner"
)

// mockMCP records calls and returns configurable responses.
type mockMCP struct {
	calls   []mcpCall
	handler func(server, tool string, args map[string]any) (any, error)
}

type mcpCall struct {
	Server string
	Tool   string
	Args   map[string]any
}

func (m *mockMCP) Call(_ context.Context, server, tool string, args map[string]any) (any, error) {
	m.calls = append(m.calls, mcpCall{Server: server, Tool: tool, Args: args})
	if m.handler != nil {
		return m.handler(server, tool, args)
	}
	return map[string]any{"ok": true}, nil
}

func workflowPlan(steps ...*planner.GoComputation) *planner.QueryPlan {
	plan := &planner.QueryPlan{BlockName: "test_workflow"}
	for _, s := range steps {
		plan.Steps = append(plan.Steps, s)
	}
	return plan
}

func mcpStep(name, server, tool string, deps []string, args map[string]ast.Expr) *planner.GoComputation {
	return &planner.GoComputation{
		Function: "mcp_call",
		Input:    "",
		Params: map[string]any{
			"step":       name,
			"depends_on": deps,
			"mcp":        &ast.MCPCall{Server: server, Tool: tool, Args: args},
		},
		Into: name + "_result",
	}
}

func TestWorkflowMCPDispatch(t *testing.T) {
	mock := &mockMCP{}
	e := &Executor{MCP: mock}
	ctx := context.Background()

	plan := workflowPlan(
		mcpStep("create", "hr", "create-person", nil, map[string]ast.Expr{
			"name": &ast.LiteralExpr{Value: "Alice"},
		}),
	)

	result, err := e.Run(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}

	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 MCP call, got %d", len(mock.calls))
	}
	if mock.calls[0].Server != "hr" || mock.calls[0].Tool != "create-person" {
		t.Errorf("call: %+v", mock.calls[0])
	}
	if mock.calls[0].Args["name"] != "Alice" {
		t.Errorf("args: %+v", mock.calls[0].Args)
	}
	if len(result.Steps) != 1 {
		t.Errorf("result steps: %d", len(result.Steps))
	}
}

func TestWorkflowStepResultChaining(t *testing.T) {
	mock := &mockMCP{
		handler: func(server, tool string, args map[string]any) (any, error) {
			if tool == "create-person" {
				return map[string]any{"result": map[string]any{"id": 42}}, nil
			}
			return map[string]any{"ok": true}, nil
		},
	}
	e := &Executor{MCP: mock}
	ctx := context.Background()

	plan := workflowPlan(
		mcpStep("create", "hr", "create-person", nil, map[string]ast.Expr{
			"name": &ast.LiteralExpr{Value: "Alice"},
		}),
		mcpStep("assign", "inv", "assign-item", []string{"create"}, map[string]ast.Expr{
			"person_id": &ast.StepResultExpr{StepName: "create", Field: "result.id"},
		}),
	)

	_, err := e.Run(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}

	if len(mock.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(mock.calls))
	}
	// Step 2 should have resolved step("create").result.id → 42
	if mock.calls[1].Args["person_id"] != 42 {
		t.Errorf("person_id: got %v, want 42", mock.calls[1].Args["person_id"])
	}
}

func TestWorkflowConfirmDeny(t *testing.T) {
	mock := &mockMCP{}
	hook := func(_ context.Context, step, server, tool string) (bool, error) {
		return false, nil // deny all
	}
	e := &Executor{MCP: mock, ConfirmHook: hook}
	ctx := context.Background()

	plan := workflowPlan(
		mcpStep("delete", "srv", "delete-all", nil, map[string]ast.Expr{
			"confirm": &ast.LiteralExpr{Value: true},
		}),
	)

	result, err := e.Run(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}

	if len(mock.calls) != 0 {
		t.Errorf("expected 0 MCP calls when denied, got %d", len(mock.calls))
	}
	out, ok := result.Vars["delete_result"].(map[string]any)
	if !ok {
		t.Fatal("missing delete_result")
	}
	if out["status"] != "skipped" {
		t.Errorf("status: got %v, want skipped", out["status"])
	}
}

func TestWorkflowCollectAll(t *testing.T) {
	page := 0
	mock := &mockMCP{
		handler: func(server, tool string, args map[string]any) (any, error) {
			page++
			if page < 3 {
				return map[string]any{
					"items":    []any{map[string]any{"id": page * 10}},
					"has_more": true,
				}, nil
			}
			return map[string]any{
				"items":    []any{map[string]any{"id": page * 10}},
				"has_more": false,
			}, nil
		},
	}
	e := &Executor{MCP: mock}
	ctx := context.Background()

	plan := workflowPlan(
		mcpStep("find", "srv", "list", nil, map[string]ast.Expr{
			"query":       &ast.LiteralExpr{Value: "test"},
			"collect_all": &ast.LiteralExpr{Value: true},
		}),
	)

	result, err := e.Run(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}

	out, ok := result.Vars["find_result"].(map[string]any)
	if !ok {
		t.Fatal("missing find_result")
	}
	items, ok := out["items"].([]any)
	if !ok {
		t.Fatal("missing items")
	}
	if len(items) != 3 {
		t.Errorf("expected 3 items (3 pages), got %d", len(items))
	}
	// collect_all should not be passed to the MCP caller
	for _, c := range mock.calls {
		if _, has := c.Args["collect_all"]; has {
			t.Error("collect_all should be stripped from MCP args")
		}
	}
}

func TestWorkflowMapExpr(t *testing.T) {
	mock := &mockMCP{
		handler: func(server, tool string, args map[string]any) (any, error) {
			if tool == "list" {
				return map[string]any{
					"result": map[string]any{
						"items": []any{
							map[string]any{"id": 1, "name": "a"},
							map[string]any{"id": 2, "name": "b"},
							map[string]any{"id": 3, "name": "c"},
						},
					},
				}, nil
			}
			return map[string]any{"ok": true}, nil
		},
	}
	e := &Executor{MCP: mock}
	ctx := context.Background()

	plan := workflowPlan(
		mcpStep("find", "srv", "list", nil, map[string]ast.Expr{
			"query": &ast.LiteralExpr{Value: "test"},
		}),
		mcpStep("delete", "srv", "batch-delete", []string{"find"}, map[string]ast.Expr{
			"ids": &ast.MapExpr{
				Source: &ast.StepResultExpr{StepName: "find", Field: "result.items"},
				Field:  "id",
			},
		}),
	)

	_, err := e.Run(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}

	if len(mock.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(mock.calls))
	}
	ids, ok := mock.calls[1].Args["ids"].([]any)
	if !ok {
		t.Fatalf("ids: expected []any, got %T", mock.calls[1].Args["ids"])
	}
	if len(ids) != 3 {
		t.Errorf("expected 3 ids, got %d", len(ids))
	}
	if ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Errorf("ids: got %v", ids)
	}
}

func TestWorkflowNoToolResolverStub(t *testing.T) {
	e := &Executor{} // no MCP caller
	ctx := context.Background()

	plan := workflowPlan(
		mcpStep("s1", "srv", "tool", nil, map[string]ast.Expr{
			"x": &ast.LiteralExpr{Value: 1},
		}),
	)

	result, err := e.Run(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}

	out, ok := result.Vars["s1_result"].(map[string]any)
	if !ok {
		t.Fatal("missing s1_result")
	}
	if out["status"] != "stub" {
		t.Errorf("expected stub status, got %v", out["status"])
	}
}

func TestWorkflowMCPError(t *testing.T) {
	mock := &mockMCP{
		handler: func(server, tool string, args map[string]any) (any, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}
	e := &Executor{MCP: mock}
	ctx := context.Background()

	plan := workflowPlan(
		mcpStep("s1", "srv", "tool", nil, map[string]ast.Expr{}),
	)

	_, err := e.Run(ctx, plan)
	if err == nil {
		t.Fatal("expected error")
	}
}


package executor

import (
	"context"
	"testing"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/lexer"
	"github.com/opentalon/tln-language/internal/parser"
	"github.com/opentalon/tln-language/internal/planner"
	"github.com/opentalon/tln-language/internal/validator"
)

// compilePlans runs the full front end (lex → parse → validate → plan) on src
// so the workflow-step `when` guard is exercised end-to-end, not just as a
// hand-built plan.
func compilePlans(t *testing.T, src string) map[string]*planner.QueryPlan {
	t.Helper()
	tokens, ld := lexer.Lex("guard.tln", src)
	if ld.HasErrors() {
		t.Fatalf("lex: %v", ld)
	}
	prog, pd := parser.Parse("guard.tln", tokens)
	if pd.HasErrors() {
		t.Fatalf("parse: %v", pd)
	}
	if vd := validator.Validate("guard.tln", prog); vd.HasErrors() {
		t.Fatalf("validate: %v", vd)
	}
	plans, planDiags := planner.Plan(prog)
	if planDiags.HasErrors() {
		t.Fatalf("plan: %v", planDiags)
	}
	return plans
}

// A "search → decide → act" dedup workflow: only create a repair ticket when a
// prior search found no open tickets for the item.
const dedupWorkflow = `
workflow "dedup" {
  step "search" {
    tool "timly" "list-tickets" { query "item_ids:42" }
  }
  step "create" depends_on "search" {
    when length(step("search").tickets) == 0
    tool "timly" "create-ticket" { title "Repair" }
  }
}
`

func TestStepGuardSkipsWhenSearchHasResults(t *testing.T) {
	mock := &mockMCP{handler: func(_, tool string, _ map[string]any) (any, error) {
		if tool == "list-tickets" {
			return map[string]any{"tickets": []any{map[string]any{"id": 7}}}, nil
		}
		return map[string]any{"ok": true}, nil
	}}
	e := &Executor{Tools: mock}
	plans := compilePlans(t, dedupWorkflow)

	if _, err := e.Run(context.Background(), plans["dedup"]); err != nil {
		t.Fatal(err)
	}

	for _, c := range mock.calls {
		if c.Tool == "create-ticket" {
			t.Fatalf("create-ticket must be skipped when an open ticket exists; calls=%+v", mock.calls)
		}
	}
	if len(mock.calls) != 1 {
		t.Fatalf("expected only the search call, got %+v", mock.calls)
	}
}

func TestStepGuardRunsWhenSearchEmpty(t *testing.T) {
	mock := &mockMCP{handler: func(_, tool string, _ map[string]any) (any, error) {
		if tool == "list-tickets" {
			return map[string]any{"tickets": []any{}}, nil
		}
		return map[string]any{"ok": true}, nil
	}}
	e := &Executor{Tools: mock}
	plans := compilePlans(t, dedupWorkflow)

	if _, err := e.Run(context.Background(), plans["dedup"]); err != nil {
		t.Fatal(err)
	}

	var created bool
	for _, c := range mock.calls {
		if c.Tool == "create-ticket" {
			created = true
		}
	}
	if !created {
		t.Fatalf("create-ticket must run when the search found nothing; calls=%+v", mock.calls)
	}
}

// A list-literal MCP arg (assignee_ids [2383]) must resolve to a real []any,
// not nil — the gap that blocked reactive create-ticket authoring.
func TestListLiteralMCPArg(t *testing.T) {
	var got map[string]any
	mock := &mockMCP{handler: func(_, tool string, args map[string]any) (any, error) {
		got = args
		return map[string]any{"ok": true}, nil
	}}
	e := &Executor{Tools: mock}
	plan := workflowPlan(
		mcpStep("create", "timly", "create-ticket", nil, map[string]ast.Expr{
			"name":         &ast.LiteralExpr{Value: "Repair"},
			"assignee_ids": &ast.ListExpr{Elements: []ast.Expr{&ast.LiteralExpr{Value: 2383}}},
			"item_ids":     &ast.ListExpr{Elements: []ast.Expr{&ast.LiteralExpr{Value: 1}, &ast.LiteralExpr{Value: 2}}},
		}),
	)
	if _, err := e.Run(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	ids, ok := got["assignee_ids"].([]any)
	if !ok || len(ids) != 1 || ids[0] != 2383 {
		t.Fatalf("assignee_ids: want []any{2383}, got %#v", got["assignee_ids"])
	}
	if items, ok := got["item_ids"].([]any); !ok || len(items) != 2 {
		t.Fatalf("item_ids: want 2 elements, got %#v", got["item_ids"])
	}
}

// `.find(field == value).field` picks a specific element of a list result by a
// field value — resolving e.g. a ticket category by name instead of a fragile
// positional index.
func TestFindByFieldMCPArg(t *testing.T) {
	var got map[string]any
	mock := &mockMCP{handler: func(_, tool string, args map[string]any) (any, error) {
		if tool == "list-ticket-categories" {
			return map[string]any{"ticket_categories": []any{
				map[string]any{"id": 1, "name": "Test"},
				map[string]any{"id": 2, "name": "Defect"},
			}}, nil
		}
		got = args
		return map[string]any{"ok": true}, nil
	}}
	e := &Executor{Tools: mock}
	plan := compilePlans(t, `
workflow "w" {
  step "cats" { tool "timly" "list-ticket-categories" {} }
  step "create" depends_on "cats" {
    tool "timly" "create-ticket" {
      name "x"
      ticket_category_id step("cats").ticket_categories.find(name == "Defect").id
    }
  }
}`)
	if _, err := e.Run(context.Background(), plan["w"]); err != nil {
		t.Fatal(err)
	}
	if got["ticket_category_id"] != 2 {
		t.Fatalf("ticket_category_id: want 2 (Defect), got %#v", got["ticket_category_id"])
	}
}

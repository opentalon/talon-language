package talon_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/opentalon/talon-language/pkg/talon"
)

// mockCaller records every MCP invocation and lets a per-test handler
// shape the response.
type mockCaller struct {
	calls   []mcpCall
	handler func(server, tool string, args map[string]any) (any, error)
}

type mcpCall struct {
	Server string
	Tool   string
	Args   map[string]any
}

func (m *mockCaller) Call(_ context.Context, server, tool string, args map[string]any) (any, error) {
	m.calls = append(m.calls, mcpCall{Server: server, Tool: tool, Args: args})
	if m.handler != nil {
		return m.handler(server, tool, args)
	}
	return map[string]any{"ok": true}, nil
}

func TestRunWorkflow_MCPDispatch(t *testing.T) {
	src := `
workflow "create" {
  step "create" {
    mcp "hr" "create-person" {
      name "Alice"
    }
  }
}`
	mock := &mockCaller{}
	result, err := talon.RunWorkflow(context.Background(), src, talon.WithMCP(mock))
	if err != nil {
		t.Fatalf("RunWorkflow: %v", err)
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

	block, ok := result.Blocks["create"]
	if !ok {
		t.Fatalf("missing block result; have %v", keys(result.Blocks))
	}
	if len(block.Steps) != 1 {
		t.Errorf("block steps: got %d, want 1", len(block.Steps))
	}
}

func TestRunWorkflow_StepResultChaining(t *testing.T) {
	src := `
workflow "chain" {
  step "create" {
    mcp "hr" "create-person" {
      name "Alice"
    }
  }
  step "assign" depends_on "create" {
    mcp "inv" "assign-item" {
      person_id step("create").result.id
    }
  }
}`
	mock := &mockCaller{
		handler: func(_, tool string, _ map[string]any) (any, error) {
			if tool == "create-person" {
				return map[string]any{"result": map[string]any{"id": 42}}, nil
			}
			return map[string]any{"ok": true}, nil
		},
	}
	if _, err := talon.RunWorkflow(context.Background(), src, talon.WithMCP(mock)); err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}

	if len(mock.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(mock.calls))
	}
	if mock.calls[1].Args["person_id"] != 42 {
		t.Errorf("person_id: got %v, want 42", mock.calls[1].Args["person_id"])
	}
}

func TestRunWorkflow_ConfirmDeny(t *testing.T) {
	src := `
workflow "deny" {
  step "delete" {
    mcp "srv" "delete-all" {
      confirm true
    }
  }
}`
	mock := &mockCaller{}
	hook := func(_ context.Context, _, _, _ string) (bool, error) { return false, nil }

	result, err := talon.RunWorkflow(context.Background(), src,
		talon.WithMCP(mock),
		talon.WithConfirmHook(hook),
	)
	if err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}
	if len(mock.calls) != 0 {
		t.Errorf("expected 0 MCP calls when denied, got %d", len(mock.calls))
	}
	out, ok := result.Blocks["deny"].Vars["delete_result"].(map[string]any)
	if !ok {
		t.Fatal("missing delete_result")
	}
	if out["status"] != "skipped" {
		t.Errorf("status: got %v, want skipped", out["status"])
	}
}

func TestRunWorkflow_CollectAll(t *testing.T) {
	src := `
workflow "paginate" {
  step "find" {
    mcp "srv" "list" {
      query "test"
      collect_all true
    }
  }
}`
	page := 0
	mock := &mockCaller{
		handler: func(_, _ string, args map[string]any) (any, error) {
			if _, has := args["collect_all"]; has {
				t.Error("collect_all should be stripped from MCP args")
			}
			page++
			return map[string]any{
				"items":    []any{map[string]any{"id": page * 10}},
				"has_more": page < 3,
			}, nil
		},
	}

	result, err := talon.RunWorkflow(context.Background(), src, talon.WithMCP(mock))
	if err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}

	out, ok := result.Blocks["paginate"].Vars["find_result"].(map[string]any)
	if !ok {
		t.Fatal("missing find_result")
	}
	items, ok := out["items"].([]any)
	if !ok {
		t.Fatal("missing items")
	}
	if len(items) != 3 {
		t.Errorf("expected 3 items across 3 pages, got %d", len(items))
	}
}

func TestRunWorkflow_MapExpr(t *testing.T) {
	src := `
workflow "map" {
  step "find" {
    mcp "srv" "list" {
      query "test"
    }
  }
  step "delete" depends_on "find" {
    mcp "srv" "batch-delete" {
      ids step("find").result.items.map(id)
    }
  }
}`
	mock := &mockCaller{
		handler: func(_, tool string, _ map[string]any) (any, error) {
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

	if _, err := talon.RunWorkflow(context.Background(), src, talon.WithMCP(mock)); err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}
	if len(mock.calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(mock.calls))
	}
	ids, ok := mock.calls[1].Args["ids"].([]any)
	if !ok {
		t.Fatalf("ids: expected []any, got %T", mock.calls[1].Args["ids"])
	}
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Errorf("ids: got %v", ids)
	}
}

func TestRunWorkflow_NoMCPCaller(t *testing.T) {
	src := `
workflow "stub" {
  step "s1" {
    mcp "srv" "tool" {
      x 1
    }
  }
}`
	result, err := talon.RunWorkflow(context.Background(), src) // no WithMCP
	if err != nil {
		t.Fatalf("RunWorkflow: %v", err)
	}
	out, ok := result.Blocks["stub"].Vars["s1_result"].(map[string]any)
	if !ok {
		t.Fatal("missing s1_result")
	}
	if out["status"] != "stub" {
		t.Errorf("status: got %v, want stub", out["status"])
	}
}

func TestRunWorkflow_MCPError(t *testing.T) {
	src := `
workflow "err" {
  step "s1" {
    mcp "srv" "tool" {
      x 1
    }
  }
}`
	mock := &mockCaller{
		handler: func(_, _ string, _ map[string]any) (any, error) {
			return nil, errors.New("connection refused")
		},
	}
	_, err := talon.RunWorkflow(context.Background(), src, talon.WithMCP(mock))
	if err == nil {
		t.Fatal("expected error from MCP failure")
	}
	if _, isCompile := err.(*talon.CompileError); isCompile {
		t.Errorf("expected runtime error, got CompileError: %v", err)
	}
}

func TestRunWorkflow_CompileError_Parse(t *testing.T) {
	src := `workflow "broken" { step "s1" {` // unterminated braces
	_, err := talon.RunWorkflow(context.Background(), src)
	if err == nil {
		t.Fatal("expected compile error")
	}
	ce, ok := err.(*talon.CompileError)
	if !ok {
		t.Fatalf("expected *CompileError, got %T: %v", err, err)
	}
	if ce.Stage != "parse" {
		t.Errorf("Stage: got %q, want parse", ce.Stage)
	}
	if len(ce.Diags) == 0 {
		t.Error("expected at least one diagnostic")
	}
}

func TestRunWorkflow_FilenameOption(t *testing.T) {
	src := `workflow "broken" { step "s1" {`
	_, err := talon.RunWorkflow(context.Background(), src, talon.WithFilename("myfile.tln"))
	if err == nil {
		t.Fatal("expected compile error")
	}
	if !strings.Contains(err.Error(), "myfile.tln") {
		t.Errorf("error should reference filename: %v", err)
	}
}

func keys(m map[string]*talon.BlockResult) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Compile-time check: mockCaller satisfies talon.MCPCaller.
var _ talon.MCPCaller = (*mockCaller)(nil)

// Compile-time guard documenting the public surface — bare references
// so any rename in the SDK breaks the test build instead of silently
// going through.
var (
	_ = talon.RunWorkflow
	_ = talon.WithMCP
	_ = talon.WithConfirmHook
	_ = talon.WithFilename
	_ = talon.SeverityError
	_ = talon.SeverityWarning
	_ = talon.SeverityInfo
	_ talon.Result
	_ talon.BlockResult
	_ talon.StepResult
	_ talon.Diagnostic
	_ fmt.Stringer = talon.Diagnostic{}
)

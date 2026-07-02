package executor

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/opentalon/talon-language/internal/ast"
	talonlog "github.com/opentalon/talon-language/internal/log"
)

// failNTimes returns a handler that errors on the first n calls, then
// succeeds. Paired with mockMCP.calls to assert the attempt count.
func failNTimes(n int) func(string, string, map[string]any) (any, error) {
	var seen int
	return func(_, _ string, _ map[string]any) (any, error) {
		seen++
		if seen <= n {
			return nil, errors.New("transient boom")
		}
		return map[string]any{"ok": true}, nil
	}
}

func TestDispatchMCP_RetriesThenSucceeds(t *testing.T) {
	mock := &mockMCP{handler: failNTimes(2)} // fail twice, succeed on the 3rd
	e := &Executor{MCP: mock}
	oe := &ast.OnErrorClause{Actions: []ast.ErrorAction{&ast.RetryAction{Times: 3}}}

	res, skipped, err := e.dispatchMCP(context.Background(), "s", "t", nil, oe, nil)
	if err != nil || skipped {
		t.Fatalf("expected success, got skipped=%v err=%v", skipped, err)
	}
	if len(mock.calls) != 3 {
		t.Errorf("expected 3 attempts (1 + 2 retries used), got %d", len(mock.calls))
	}
	if m, ok := res.(map[string]any); !ok || m["ok"] != true {
		t.Errorf("unexpected result: %v", res)
	}
}

func TestDispatchMCP_RetryExhaustedDefaultsToFail(t *testing.T) {
	mock := &mockMCP{handler: func(_, _ string, _ map[string]any) (any, error) {
		return nil, errors.New("always down")
	}}
	e := &Executor{MCP: mock}
	oe := &ast.OnErrorClause{Actions: []ast.ErrorAction{&ast.RetryAction{Times: 2}}} // no skip/fail → default fail

	_, skipped, err := e.dispatchMCP(context.Background(), "s", "t", nil, oe, nil)
	if skipped {
		t.Error("expected fail, not skip")
	}
	if err == nil {
		t.Error("expected error to propagate by default")
	}
	if len(mock.calls) != 3 {
		t.Errorf("expected 3 attempts (1 + 2 retries), got %d", len(mock.calls))
	}
}

func TestDispatchMCP_SkipSwallowsFailure(t *testing.T) {
	mock := &mockMCP{handler: func(_, _ string, _ map[string]any) (any, error) {
		return nil, errors.New("down")
	}}
	e := &Executor{MCP: mock}
	oe := &ast.OnErrorClause{Actions: []ast.ErrorAction{
		&ast.RetryAction{Times: 1},
		&ast.SkipAction{},
	}}

	_, skipped, err := e.dispatchMCP(context.Background(), "s", "t", nil, oe, nil)
	if !skipped || err != nil {
		t.Fatalf("expected skipped=true err=nil, got skipped=%v err=%v", skipped, err)
	}
	if len(mock.calls) != 2 {
		t.Errorf("expected 2 attempts (1 + 1 retry), got %d", len(mock.calls))
	}
}

func TestDispatchMCP_LogInterpolatesErrorAndRow(t *testing.T) {
	var buf bytes.Buffer
	orig := talonlog.Default()
	talonlog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer talonlog.SetDefault(orig)

	mock := &mockMCP{handler: func(_, _ string, _ map[string]any) (any, error) {
		return nil, errors.New("connection refused")
	}}
	e := &Executor{MCP: mock}
	oe := &ast.OnErrorClause{Actions: []ast.ErrorAction{
		&ast.LogErrorAction{Message: ast.ParseTemplate("failed for {item.name}: {error}")},
		&ast.SkipAction{},
	}}

	_, skipped, err := e.dispatchMCP(context.Background(), "s", "t", nil, oe, map[string]any{"name": "Drill"})
	if !skipped || err != nil {
		t.Fatalf("expected skip after log, got skipped=%v err=%v", skipped, err)
	}
	if out := buf.String(); !strings.Contains(out, "failed for Drill: connection refused") {
		t.Errorf("log did not interpolate row/error: %q", out)
	}
}

func TestDispatchMCP_NoPolicyFailsImmediately(t *testing.T) {
	mock := &mockMCP{handler: func(_, _ string, _ map[string]any) (any, error) {
		return nil, errors.New("boom")
	}}
	e := &Executor{MCP: mock}
	if _, _, err := e.dispatchMCP(context.Background(), "s", "t", nil, nil, nil); err == nil {
		t.Error("no on_error clause should fail on first error")
	}
	if len(mock.calls) != 1 {
		t.Errorf("no retry expected, got %d attempts", len(mock.calls))
	}
}

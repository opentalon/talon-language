package testrunner

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/executor"
	"github.com/opentalon/tln-language/internal/planner"
)

// recordingCaller is the test double installed for `mock mcp` clauses. It
// answers calls from the matching MockClause (canned response or error)
// and records every invocation for `mcp_called` assertions. Unmocked
// calls return an empty success so the block under test proceeds.
type recordingCaller struct {
	mocks []ast.MockClause
	seen  map[string]int // (server,tool) -> successful-call count, for `fails after N`
	calls []recordedCall
}

type recordedCall struct {
	server string
	tool   string
	args   map[string]any
}

func newRecordingCaller(mocks []ast.MockClause) *recordingCaller {
	return &recordingCaller{mocks: mocks, seen: map[string]int{}}
}

func (r *recordingCaller) Call(_ context.Context, server, tool string, args map[string]any) (any, error) {
	r.calls = append(r.calls, recordedCall{server: server, tool: tool, args: args})
	m := r.mockFor(server, tool)
	if m == nil {
		return map[string]any{}, nil // unmocked: lenient success
	}
	if m.Fails {
		key := server + "\x00" + tool
		if m.FailAfter > 0 && r.seen[key] < m.FailAfter {
			r.seen[key]++
			return cloneReturns(m.Returns), nil
		}
		msg := m.FailMsg
		if msg == "" {
			msg = "mock failure"
		}
		return nil, errors.New(msg)
	}
	return cloneReturns(m.Returns), nil
}

func (r *recordingCaller) mockFor(server, tool string) *ast.MockClause {
	for i := range r.mocks {
		if r.mocks[i].Server == server && r.mocks[i].Tool == tool {
			return &r.mocks[i]
		}
	}
	return nil
}

func cloneReturns(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// runMCPAssertions runs the block through the real executor with a
// recording caller, then checks each mcp_called assertion. Returned
// strings are assertion-failure messages (empty slice = all passed).
func runMCPAssertions(tb *ast.TestBlock, plan *planner.QueryPlan, entities map[int]*entity) []string {
	if plan == nil {
		return []string{fmt.Sprintf("test %q: no plan for block %q to check mcp calls", tb.Name, tb.WhenBlock)}
	}
	rec := newRecordingCaller(tb.Mocks)
	exec := executor.NewExecutor(storeFromEntities(entities))
	exec.MCP = rec
	if _, err := exec.Run(context.Background(), plan); err != nil {
		return []string{fmt.Sprintf("mcp run of %q failed: %v", tb.WhenBlock, err)}
	}

	var errs []string
	for _, want := range tb.MCPCalls {
		if msg := checkMCPCalled(want, rec.calls); msg != "" {
			errs = append(errs, msg)
		}
	}
	return errs
}

// checkMCPCalled verifies that some recorded call matches the assertion's
// server/tool and satisfies every arg predicate.
func checkMCPCalled(want ast.MCPCalledAssertion, calls []recordedCall) string {
	var matchedTarget bool
	for _, c := range calls {
		if c.server != want.Server || c.tool != want.Tool {
			continue
		}
		matchedTarget = true
		if argsSatisfy(want.Args, c.args) {
			return "" // found a fully-matching call
		}
	}
	if !matchedTarget {
		return fmt.Sprintf("expected mcp call to %q/%q, but it was not called", want.Server, want.Tool)
	}
	return fmt.Sprintf("mcp %q/%q was called, but no call matched the expected args %s",
		want.Server, want.Tool, describeArgs(want.Args))
}

func argsSatisfy(preds []ast.ArgPredicate, args map[string]any) bool {
	for _, p := range preds {
		if !argPredicateHolds(p, args[p.Name]) {
			return false
		}
	}
	return true
}

func argPredicateHolds(p ast.ArgPredicate, actual any) bool {
	switch p.Op {
	case "==":
		return valuesEqual(actual, p.Value)
	case "!=":
		return !valuesEqual(actual, p.Value)
	case "contains":
		return strings.Contains(fmt.Sprintf("%v", actual), fmt.Sprintf("%v", p.Value))
	}
	return false
}

// valuesEqual compares with numeric coercion so 501 (int) equals 501.0
// (float) — MCP args and asserted literals may differ in numeric kind.
func valuesEqual(a, b any) bool {
	if af, ok := toFloat(a); ok {
		if bf, ok := toFloat(b); ok {
			return af == bf
		}
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	}
	return 0, false
}

func describeArgs(preds []ast.ArgPredicate) string {
	parts := make([]string, 0, len(preds))
	for _, p := range preds {
		parts = append(parts, fmt.Sprintf("%s %s %v", p.Name, p.Op, p.Value))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

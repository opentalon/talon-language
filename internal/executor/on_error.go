package executor

import (
	"context"
	"time"

	"github.com/opentalon/tln-language/internal/ast"
	tlnlog "github.com/opentalon/tln-language/internal/log"
	"github.com/opentalon/tln-language/internal/template"
)

// dispatchMCP calls an MCP tool applying the call's on_error policy:
// retry N extra attempts (immediate, no backoff in v1), then run the
// log / skip / fail actions in declared order once attempts are
// exhausted. It returns (result, skipped, err):
//
//   - err == nil, skipped == false: success.
//   - skipped == true: the failure was swallowed (`skip`); treat the
//     call as a no-op and continue.
//   - err != nil: the policy chose to fail (`fail`, or the implicit
//     default when no skip/fail action is present).
//
// row supplies the context a `log` message interpolates against — its
// keys plus {error} (the failure text). It may be nil (workflow steps).
// This is the single MCP dispatch path for workflow steps, remediate,
// and enrich, so on_error behaves identically everywhere.
func (e *Executor) dispatchMCP(ctx context.Context, server, tool string, args map[string]any, oe *ast.OnErrorClause, row map[string]any) (any, bool, error) {
	attempts := 1 + retryCount(oe)
	var (
		res any
		err error
	)
	for i := 0; i < attempts; i++ {
		start := time.Now()
		res, err = e.MCP.Call(ctx, server, tool, args)
		status := "ok"
		if err != nil {
			status = "error"
		}
		tlnlog.MCPCall(ctx, server, tool, status, time.Since(start), err)
		if err == nil {
			return res, false, nil
		}
	}

	// All attempts failed — apply the fallthrough actions in order.
	if oe == nil {
		return nil, false, err // implicit fail
	}
	for _, a := range oe.Actions {
		switch act := a.(type) {
		case *ast.LogErrorAction:
			logMCPError(ctx, act, row, err)
		case *ast.SkipAction:
			return nil, true, nil
		case *ast.FailAction:
			return nil, false, err
		}
	}
	return nil, false, err // no skip/fail → default fail
}

// retryCount returns the retry count declared by the first RetryAction,
// or 0 when there is none.
func retryCount(oe *ast.OnErrorClause) int {
	if oe == nil {
		return 0
	}
	for _, a := range oe.Actions {
		if r, ok := a.(*ast.RetryAction); ok {
			return r.Times
		}
	}
	return 0
}

// logMCPError interpolates a LogErrorAction's message against the row
// context plus {error} and writes it at warn level.
func logMCPError(ctx context.Context, a *ast.LogErrorAction, row map[string]any, cause error) {
	rc := template.RenderContext{Row: rowWithError(row, cause)}
	tlnlog.Default().WarnContext(ctx, template.Render(a.Message, rc), "source", "mcp_on_error")
}

func rowWithError(row map[string]any, cause error) template.Row {
	out := make(template.Row, len(row)+1)
	for k, v := range row {
		out[k] = v
	}
	if cause != nil {
		out["error"] = cause.Error()
	}
	return out
}

package executor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/constraints"
	"github.com/opentalon/tln-language/internal/factstore"
	tlnlog "github.com/opentalon/tln-language/internal/log"
	"github.com/opentalon/tln-language/internal/planner"
	"github.com/opentalon/tln-language/internal/template"
)

// execRemediate fires a detect/recommend block's remediate body once per
// flagged row. The body is an action tree: leaf MCP calls plus imperative
// control flow (if/else, for-each, while) that branches and iterates over
// the row's entity context. Each MCP call's args resolve against that row:
//   - `attr "x"` → the typed attribute value
//   - a string literal → a per-row template, so "{item.name}", "{item.id}",
//     and "{attr.x}" interpolate (same renderer labels use)
//
// Control-flow conditions are evaluated against the same row via
// constraints.EvalCondition — the existing condition grammar, no new
// expression language. Actions run in order; if an MCP call fails (its
// on_error chose fail), the row's remaining actions are skipped. With no
// MCP caller wired the calls are no-ops (matches workflow mcp steps), so
// compiling/inspecting a program never dispatches.
func (e *Executor) execRemediate(ctx context.Context, gc *planner.GoComputation, vars map[string]any) (any, error) {
	body, _ := gc.Params["body"].([]ast.Action)
	rows, _ := vars[gc.Input].([][]any)
	mode, _ := gc.Params["mode"].(string)
	if mode == "" {
		mode = "propose"
	}
	g := &remediateGate{
		mode:      mode,
		role:      stringParam(gc.Params, "role"),
		batch:     stringParam(gc.Params, "batch"),
		blockName: stringParam(gc.Params, "block_name"),
	}
	summary := map[string]any{"fired": 0, "rows": len(rows)}
	if len(body) == 0 || len(rows) == 0 {
		return summary, nil
	}

	// Distinct entity ids, preserving flagged-row order.
	var ids []int
	seen := map[int]bool{}
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		id, ok := toEntityID(r[0])
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}

	// Fetch every attribute the body references (call args + control-flow
	// conditions + for-each collections), once for all entities.
	attrsByID := e.fetchEntityAttrs(ctx, ids, referencedAttrs(body))

	fired := 0
	for _, id := range ids {
		row := attrsByID[id]
		if row == nil {
			row = map[string]any{}
		}
		row["id"] = id
		n, _, err := e.execActions(ctx, body, row, g)
		fired += n
		if err != nil {
			return summary, err
		}
	}
	summary["fired"] = fired
	return summary, nil
}

// remediateGate carries the mode/hook policy threaded through a nested
// action body so every leaf MCP call fires under the same gating rules.
type remediateGate struct {
	mode      string
	role      string
	batch     string
	blockName string
}

// execActions walks an action body against one entity's row context,
// returning how many MCP calls fired. stop==true means the row's remaining
// actions were aborted (an MCP call's on_error chose fail); the caller
// continues with the next row. A non-nil error is fatal to the block run.
func (e *Executor) execActions(ctx context.Context, actions []ast.Action, row map[string]any, g *remediateGate) (fired int, stop bool, err error) {
	for _, act := range actions {
		switch a := act.(type) {
		case *ast.MCPAction:
			n, s, err := e.fireMCP(ctx, a.Call, row, g)
			fired += n
			if err != nil {
				return fired, true, err
			}
			if s {
				return fired, true, nil // on_error fail → stop this row
			}

		case *ast.IfAction:
			ok, err := constraints.EvalCondition(a.Cond, row)
			if err != nil {
				return fired, false, fmt.Errorf("remediate if: %w", err)
			}
			branch := a.Then
			if !ok {
				branch = a.Else
			}
			n, s, err := e.execActions(ctx, branch, row, g)
			fired += n
			if err != nil || s {
				return fired, s, err
			}

		case *ast.ForEachAction:
			items, err := evalListExpr(a.Over, row)
			if err != nil {
				return fired, false, fmt.Errorf("remediate for each %s: %w", a.Variable, err)
			}
			saved, had := row[a.Variable]
			for _, it := range items {
				row[a.Variable] = it
				n, s, err := e.execActions(ctx, a.Body, row, g)
				fired += n
				if err != nil || s {
					restoreVar(row, a.Variable, saved, had)
					return fired, s, err
				}
			}
			restoreVar(row, a.Variable, saved, had)

		case *ast.WhileAction:
			iter := 0
			for {
				ok, err := constraints.EvalCondition(a.Cond, row)
				if err != nil {
					return fired, false, fmt.Errorf("remediate while: %w", err)
				}
				if !ok {
					break
				}
				if iter >= a.MaxIter {
					return fired, true, fmt.Errorf("remediate while exceeded %d iterations without the condition changing "+
						"(no mutable loop state in a stateless remediate body — bound the loop or use `for each`)", a.MaxIter)
				}
				n, s, err := e.execActions(ctx, a.Body, row, g)
				fired += n
				if err != nil || s {
					return fired, s, err
				}
				iter++
			}
		}
	}
	return fired, false, nil
}

// fireMCP resolves one MCP call's args against the row and dispatches it
// under the gate's mode. Returns fired=1 when the call actually went out.
// A returned stop==true means the call failed and on_error chose fail, so
// the row's remaining actions should be skipped; a non-nil error is fatal.
func (e *Executor) fireMCP(ctx context.Context, call *ast.MCPCall, row map[string]any, g *remediateGate) (fired int, stop bool, err error) {
	rctx := template.RenderContext{Row: row}
	args := make(map[string]any, len(call.Args))
	for k, expr := range call.Args {
		args[k] = resolveRemediateArg(expr, row, rctx)
	}

	// queue mode never contacts MCP — it defers the call.
	if g.mode == "queue" {
		if e.Queue != nil {
			if err := e.Queue.Enqueue(ctx, g.batch, QueuedCall{Server: call.Server, Tool: call.Tool, Args: args}); err != nil {
				return 0, false, fmt.Errorf("remediate queue %s/%s: %w", call.Server, call.Tool, err)
			}
			tlnlog.MCPCall(ctx, call.Server, call.Tool, "queued", 0, nil)
			return 1, false, nil
		}
		return 0, false, nil
	}

	if e.Tools == nil {
		return 0, false, nil // no caller: stub, like workflow mcp steps
	}

	// Gate the call according to the mode.
	switch g.mode {
	case "auto":
		tlnlog.MCPCall(ctx, call.Server, call.Tool, "auto", 0, nil)
	case "approve":
		if e.ApprovalHook == nil {
			tlnlog.MCPCall(ctx, call.Server, call.Tool, "unapproved", 0, nil)
			return 0, false, nil // no approver wired → cannot approve
		}
		ok, err := e.ApprovalHook(ctx, g.role, g.blockName, args)
		if err != nil {
			return 0, false, fmt.Errorf("remediate approve %s/%s: %w", call.Server, call.Tool, err)
		}
		if !ok {
			tlnlog.MCPCall(ctx, call.Server, call.Tool, "denied", 0, nil)
			return 0, false, nil
		}
		tlnlog.MCPCall(ctx, call.Server, call.Tool, "approved", 0, nil)
	default: // propose
		if e.ConfirmHook != nil {
			proceed, err := e.ConfirmHook(ctx, call.Tool, call.Server, call.Tool)
			if err != nil {
				return 0, false, fmt.Errorf("remediate confirm %s/%s: %w", call.Server, call.Tool, err)
			}
			if !proceed {
				tlnlog.MCPCall(ctx, call.Server, call.Tool, "proposed", 0, nil)
				return 0, false, nil
			}
		}
	}

	_, skipped, err := e.dispatchMCP(ctx, call.Server, call.Tool, args, call.OnError, row)
	if err != nil {
		return 0, true, nil // on_error chose fail (or default) — stop this row's calls
	}
	if skipped {
		return 0, false, nil // on_error swallowed the failure
	}
	return 1, false, nil
}

// restoreVar puts a for-each loop variable's shadowed binding back after the
// loop, so a nested `attr`/ident of the same name outside the loop is intact.
func restoreVar(row map[string]any, name string, saved any, had bool) {
	if had {
		row[name] = saved
	} else {
		delete(row, name)
	}
}

func stringParam(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return s
}

// evalListExpr evaluates a `for each ... in <expr>` collection to a slice.
// Supports a `[...]` list literal and a list-valued `attr "x"` / bound loop
// variable; a scalar attr yields a single-element list.
func evalListExpr(e ast.Expr, row map[string]any) ([]any, error) {
	switch v := e.(type) {
	case *ast.ListExpr:
		out := make([]any, 0, len(v.Elements))
		for _, el := range v.Elements {
			out = append(out, evalScalar(el, row))
		}
		return out, nil
	case *ast.AttrExpr:
		return toList(row[v.Name]), nil
	case *ast.IdentExpr:
		return toList(row[v.Name]), nil
	default:
		return nil, fmt.Errorf("cannot iterate over %T (expected a [...] list or a list-valued attr)", e)
	}
}

// evalScalar resolves a list-element / bound-value expression to a Go value.
func evalScalar(expr ast.Expr, row map[string]any) any {
	switch v := expr.(type) {
	case *ast.LiteralExpr:
		return v.Value
	case *ast.AttrExpr:
		return row[v.Name]
	case *ast.IdentExpr:
		if val, ok := row[v.Name]; ok {
			return val
		}
		return v.Name
	default:
		return nil
	}
}

// toList coerces a fetched attribute value into an iterable slice.
func toList(v any) []any {
	switch s := v.(type) {
	case nil:
		return nil
	case []any:
		return s
	case []string:
		out := make([]any, len(s))
		for i, x := range s {
			out[i] = x
		}
		return out
	default:
		return []any{s} // scalar attr → singleton
	}
}

// referencedAttrs collects the bare attribute names the action body touches:
// MCP-call args (`attr "x"` and `{item.x}` / `{attr.x}` template refs) plus
// the attributes read by control-flow conditions and for-each collections.
// The synthetic "id" (entity id) is excluded; it's resolved directly.
func referencedAttrs(actions []ast.Action) []string {
	set := map[string]bool{}
	add := func(name string) {
		if name != "" && name != "id" {
			set[name] = true
		}
	}
	var walk func([]ast.Action)
	walk = func(as []ast.Action) {
		for _, a := range as {
			switch v := a.(type) {
			case *ast.MCPAction:
				addCallAttrs(v.Call, add)
			case *ast.IfAction:
				collectCondAttrs(v.Cond, add)
				walk(v.Then)
				walk(v.Else)
			case *ast.ForEachAction:
				collectExprAttrs(v.Over, add)
				walk(v.Body)
			case *ast.WhileAction:
				collectCondAttrs(v.Cond, add)
				walk(v.Body)
			}
		}
	}
	walk(actions)
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	return names
}

// addCallAttrs adds the attrs an MCP call reads via `attr "x"` args and via
// {item.x} / {attr.x} / {x} interpolations in string-literal args.
func addCallAttrs(call *ast.MCPCall, add func(string)) {
	for _, expr := range call.Args {
		// Any expression form (attr, arithmetic, string builtins, lists).
		collectExprAttrs(expr, add)
		// Plus {item.x} / {attr.x} template refs in string-literal args.
		if lit, ok := expr.(*ast.LiteralExpr); ok {
			s, ok := lit.Value.(string)
			if !ok || !strings.Contains(s, "{") {
				continue
			}
			for _, node := range ast.ParseTemplate(s).Nodes {
				if ref, ok := node.(*ast.RefNode); ok {
					add(templateRefAttr(ref.Path))
				}
			}
		}
	}
}

// collectCondAttrs / collectExprAttrs mirror the planner's attr walkers for
// the condition and expression forms a control-flow guard can use.
func collectCondAttrs(c ast.Condition, add func(string)) {
	switch cc := c.(type) {
	case *ast.LogicalCondition:
		collectCondAttrs(cc.Left, add)
		collectCondAttrs(cc.Right, add)
	case *ast.NotCondition:
		collectCondAttrs(cc.Inner, add)
	case *ast.CompareCondition:
		collectExprAttrs(cc.Left, add)
		collectExprAttrs(cc.Right, add)
	case *ast.MembershipCondition:
		collectExprAttrs(cc.Expr, add)
	case *ast.StringMatchCondition:
		collectExprAttrs(cc.Subject, add)
	case *ast.HasCondition:
		collectExprAttrs(cc.Subject, add)
	}
}

func collectExprAttrs(e ast.Expr, add func(string)) {
	switch ee := e.(type) {
	case *ast.AttrExpr:
		add(ee.Name)
	case *ast.BinaryExpr:
		collectExprAttrs(ee.Left, add)
		collectExprAttrs(ee.Right, add)
	case *ast.ListExpr:
		for _, el := range ee.Elements {
			collectExprAttrs(el, add)
		}
	case *ast.CallExpr:
		for _, a := range ee.Args {
			collectExprAttrs(a, add)
		}
	}
}

// templateRefAttr maps a template ref path to the bare attribute name to
// fetch: "item.name"→"name", "attr.km"→"km", "name"→"name". Paths scoped
// to context (or the entity id) return "" so they're skipped.
func templateRefAttr(path string) string {
	parts := strings.Split(path, ".")
	switch len(parts) {
	case 1:
		return parts[0]
	case 2:
		if parts[0] == "item" || parts[0] == "attr" {
			return parts[1]
		}
	}
	return ""
}

// fetchEntityAttrs returns, per entity id, a flat map of the requested
// attribute names to values. Each name is looked up in both the :attr/ and
// :record/ namespaces (an attribute asserted as either resolves), so the
// row mirrors the flattened view labels render against.
func (e *Executor) fetchEntityAttrs(ctx context.Context, ids []int, names []string) map[int]map[string]any {
	out := map[int]map[string]any{}
	if e.Client == nil || len(ids) == 0 || len(names) == 0 {
		return out
	}
	want := map[int]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, name := range names {
		// :attr/ takes precedence; :record/ fills what it doesn't.
		for _, ns := range []string{":attr/", ":record/"} {
			rows, err := e.Client.Query(ctx, factstore.Query{
				Find: []string{"?e", "?v"},
				Where: []factstore.Clause{&factstore.Pattern{
					Entity:    factstore.Var("?e"),
					Attribute: ns + name,
					Value:     factstore.Var("?v"),
				}},
			})
			if err != nil {
				continue
			}
			for _, r := range rows {
				if len(r) < 2 {
					continue
				}
				id, ok := toEntityID(r[0])
				if !ok || !want[id] {
					continue
				}
				m := out[id]
				if m == nil {
					m = map[string]any{}
					out[id] = m
				}
				if _, present := m[name]; !present {
					m[name] = r[1]
				}
			}
		}
	}
	return out
}

// resolveRemediateArg evaluates one MCP arg expression against a flagged
// row's entity context.
func resolveRemediateArg(expr ast.Expr, row map[string]any, rctx template.RenderContext) any {
	switch v := expr.(type) {
	case *ast.LiteralExpr:
		if s, ok := v.Value.(string); ok {
			if strings.Contains(s, "{") {
				return template.Render(ast.ParseTemplate(s), rctx)
			}
			return s
		}
		return v.Value
	case *ast.AttrExpr:
		return row[v.Name]
	case *ast.IdentExpr:
		if val, ok := row[v.Name]; ok {
			return val
		}
		return v.Name
	default:
		// Arithmetic, string builtins, etc. — resolve through the shared
		// value evaluator so MCP args support the full expression language.
		if val, err := constraints.EvalExpr(expr, row, time.Now().UTC()); err == nil {
			return val
		}
		return nil
	}
}

// toEntityID coerces a query-bound entity id (float64 in MemoryStore) to int.
func toEntityID(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	}
	return 0, false
}

package executor

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/factstore"
	talonlog "github.com/opentalon/talon-language/internal/log"
	"github.com/opentalon/talon-language/internal/planner"
	"github.com/opentalon/talon-language/internal/template"
)

// execRemediate fires a detect/recommend block's remediate MCP calls once
// per flagged row. Each call's args resolve against that row's entity:
//   - `attr "x"` → the typed attribute value
//   - a string literal → a per-row template, so "{item.name}", "{item.id}",
//     and "{attr.x}" interpolate (same renderer labels use)
//
// Calls run in order; if one fails, the remaining calls for that row are
// skipped. With no MCP caller wired the step is a no-op (matches workflow
// mcp steps), so compiling/inspecting a program never dispatches.
func (e *Executor) execRemediate(ctx context.Context, gc *planner.GoComputation, vars map[string]any) (any, error) {
	calls, _ := gc.Params["calls"].([]*ast.MCPCall)
	rows, _ := vars[gc.Input].([][]any)
	summary := map[string]any{"fired": 0, "rows": len(rows)}
	if len(calls) == 0 || len(rows) == 0 {
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

	// Fetch every attribute the calls reference, once for all entities.
	attrsByID := e.fetchEntityAttrs(ctx, ids, referencedAttrs(calls))

	fired := 0
	for _, id := range ids {
		row := attrsByID[id]
		if row == nil {
			row = map[string]any{}
		}
		row["id"] = id
		rctx := template.RenderContext{Row: row}

		for _, call := range calls {
			args := make(map[string]any, len(call.Args))
			for k, expr := range call.Args {
				args[k] = resolveRemediateArg(expr, row, rctx)
			}
			if e.MCP == nil {
				continue // no caller: stub, like workflow mcp steps
			}
			if e.ConfirmHook != nil {
				proceed, err := e.ConfirmHook(ctx, call.Tool, call.Server, call.Tool)
				if err != nil {
					return summary, fmt.Errorf("remediate confirm %s/%s: %w", call.Server, call.Tool, err)
				}
				if !proceed {
					talonlog.MCPCall(ctx, call.Server, call.Tool, "skipped", 0, nil)
					continue
				}
			}
			_, skipped, err := e.dispatchMCP(ctx, call.Server, call.Tool, args, call.OnError, row)
			if err != nil {
				break // on_error chose fail (or default) — stop this row's calls
			}
			if skipped {
				continue // on_error swallowed the failure
			}
			fired++
		}
	}
	summary["fired"] = fired
	return summary, nil
}

// referencedAttrs collects the bare attribute names the calls reference —
// from `attr "x"` args and from `{item.x}` / `{attr.x}` / `{x}` template
// refs in string-literal args. The synthetic "id" (entity id) is excluded;
// it's resolved directly, not fetched.
func referencedAttrs(calls []*ast.MCPCall) []string {
	set := map[string]bool{}
	add := func(name string) {
		if name != "" && name != "id" {
			set[name] = true
		}
	}
	for _, call := range calls {
		for _, expr := range call.Args {
			switch v := expr.(type) {
			case *ast.AttrExpr:
				add(v.Name)
			case *ast.LiteralExpr:
				s, ok := v.Value.(string)
				if !ok || !strings.Contains(s, "{") {
					continue
				}
				for _, node := range ast.ParseTemplate(s).Nodes {
					ref, ok := node.(*ast.RefNode)
					if !ok {
						continue
					}
					add(templateRefAttr(ref.Path))
				}
			}
		}
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	return names
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

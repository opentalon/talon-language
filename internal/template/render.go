// Package template renders ast.Template values against a runtime
// RenderContext. The template syntax is documented in
// docs/spec/v0.2.md section 9 and in the AST package's ParseTemplate
// doc. See issue #60.
//
// The renderer is intentionally pure (no I/O) — the caller supplies the
// already-resolved row, the aggregate scope, and `now`. That keeps the
// renderer easy to test and keeps it usable from any plan-step type
// that has a matched-rows scope (Tier-1 Decisions today; the executor's
// render_template GoComputation in a follow-up).
package template

import (
	"fmt"
	"strings"
	"time"

	"github.com/opentalon/talon-language/internal/ast"
)

// Row represents one matched record's resolved attributes. The keys are
// the unprefixed attribute names ("name", "km", "current_stock"); the
// renderer prepends ":record/" / ":attr/" internally based on the
// reference shape (`item.x` vs `attr.x` vs bare `x`).
type Row map[string]any

// RenderContext gives the renderer enough scope to resolve every
// template function. Fields:
//
//   - Row — the per-row data for `{item.name}`, `{attr.x}`, bare refs.
//   - AggregateRows — the full matched set, used for `{count}`,
//     `{total(attr.x)}`, `{avg(attr.x)}`, etc.  Each element is a Row
//     keyed the same way as Row.
//   - Context — `{context.role}` style runtime variables.
//   - Now — substituted into `{days_until(date)}` / `{days_since(date)}`.
//     Tests pass a fixed clock; callers usually pass `time.Now()`.
type RenderContext struct {
	Row           Row
	AggregateRows []Row
	Context       map[string]any
	// Calc holds `calculate` scalars by variable name, so a label can
	// interpolate `{daily_rate}` from an aggregation clause.
	Calc map[string]float64
	Now  time.Time
}

// Render evaluates the template against the context.
//
// Unresolved references and unknown functions are emitted as their
// original `{...}` form so the rendered output makes the gap visible.
// Errors aren't returned — callers using this for Decision / label text
// want best-effort output, not a fail-fast. The validator already
// rejects unknown functions at compile time (when ParseTemplate
// populated Nodes); this fallback is for templates constructed without
// going through ParseTemplate, or for refs to attributes that simply
// aren't on the matched row.
func Render(t ast.Template, ctx RenderContext) string {
	// Fast path: Nodes wasn't populated (e.g. a hand-constructed
	// Template in a test). Fall back to the raw string with no
	// interpolation — same behaviour as the legacy regex renderer for
	// the trivial "no braces" case.
	if len(t.Nodes) == 0 {
		return t.Raw
	}
	var b strings.Builder
	for _, n := range t.Nodes {
		switch node := n.(type) {
		case *ast.LiteralNode:
			b.WriteString(node.Text)
		case *ast.RefNode:
			b.WriteString(resolveRef(node.Path, ctx))
		case *ast.FuncNode:
			b.WriteString(resolveFunc(node, ctx))
		}
	}
	return b.String()
}

// resolveRef handles `{item.name}`, `{attr.km}`, `{context.role}`, and
// bare `{name}` references.
func resolveRef(path string, ctx RenderContext) string {
	switch {
	case path == "item.name":
		if v, ok := ctx.Row["name"]; ok {
			return formatValue(v)
		}
	case strings.HasPrefix(path, "attr."):
		key := strings.TrimPrefix(path, "attr.")
		if v, ok := ctx.Row[key]; ok {
			return formatValue(v)
		}
	case strings.HasPrefix(path, "context."):
		key := strings.TrimPrefix(path, "context.")
		if v, ok := ctx.Context[key]; ok {
			return formatValue(v)
		}
	default:
		// Bare key — record/attr fields first, then calculate scalars.
		if v, ok := ctx.Row[path]; ok {
			return formatValue(v)
		}
		if v, ok := ctx.Calc[path]; ok {
			return formatValue(v)
		}
	}
	return "{" + path + "}"
}

// resolveFunc dispatches on the function name. Argument-count
// validation happens at the validator pass; renders defensively if it
// missed something.
func resolveFunc(n *ast.FuncNode, ctx RenderContext) string {
	switch n.Fn {
	case "count":
		return formatValue(float64(len(ctx.AggregateRows)))
	case "total", "sum":
		if len(n.Args) != 1 {
			return originalCall(n)
		}
		sum, _ := aggregateNumeric(n.Args[0], ctx.AggregateRows)
		return formatValue(sum)
	case "avg":
		if len(n.Args) != 1 {
			return originalCall(n)
		}
		sum, count := aggregateNumeric(n.Args[0], ctx.AggregateRows)
		if count == 0 {
			return "0"
		}
		return formatValue(sum / float64(count))
	case "min":
		if len(n.Args) != 1 {
			return originalCall(n)
		}
		return formatValue(aggregateExtreme(n.Args[0], ctx.AggregateRows, true))
	case "max":
		if len(n.Args) != 1 {
			return originalCall(n)
		}
		return formatValue(aggregateExtreme(n.Args[0], ctx.AggregateRows, false))
	case "days_until":
		if len(n.Args) != 1 {
			return originalCall(n)
		}
		return formatValue(daysBetween(ctx.Row, n.Args[0], ctx.Now, +1))
	case "days_since":
		if len(n.Args) != 1 {
			return originalCall(n)
		}
		return formatValue(daysBetween(ctx.Row, n.Args[0], ctx.Now, -1))
	}
	return originalCall(n)
}

// aggregateNumeric returns sum + count of numeric values found at
// `path` (e.g. "attr.km") across all aggregate rows.
func aggregateNumeric(path string, rows []Row) (float64, int) {
	key := strings.TrimPrefix(path, "attr.")
	var sum float64
	count := 0
	for _, r := range rows {
		v, ok := r[key]
		if !ok {
			continue
		}
		if f, ok := toFloat(v); ok {
			sum += f
			count++
		}
	}
	return sum, count
}

// aggregateExtreme returns the min (when wantMin=true) or max numeric
// value at `path` across the rows. Returns 0 when no rows contain a
// numeric value at that path.
func aggregateExtreme(path string, rows []Row, wantMin bool) any {
	key := strings.TrimPrefix(path, "attr.")
	var best float64
	seen := false
	for _, r := range rows {
		v, ok := r[key]
		if !ok {
			continue
		}
		f, ok := toFloat(v)
		if !ok {
			continue
		}
		if !seen || (wantMin && f < best) || (!wantMin && f > best) {
			best = f
			seen = true
		}
	}
	if !seen {
		return float64(0)
	}
	return best
}

// daysBetween reads a date-like value from the row at `path` and
// computes how many calendar days from / until `now`. Direction = +1
// for days_until, -1 for days_since (so the result is non-negative for
// dates in the future / past respectively).
//
// Both `now` and the target are normalised to UTC date-only (midnight)
// before subtraction so the result matches human calendar arithmetic:
// `days_until` from 2026-06-01T12:00 to 2026-06-08T00:00 reads as 7,
// not 6.
func daysBetween(row Row, path string, now time.Time, direction int) any {
	key := strings.TrimPrefix(path, "attr.")
	v, ok := row[key]
	if !ok {
		return "{?}"
	}
	target, ok := toTime(v)
	if !ok {
		return "{?}"
	}
	nowDay := dateOnlyUTC(now)
	targetDay := dateOnlyUTC(target)
	diff := targetDay.Sub(nowDay)
	days := int(diff / (24 * time.Hour))
	if direction < 0 {
		days = -days
	}
	if days < 0 {
		days = 0
	}
	return float64(days)
}

func dateOnlyUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func toTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01-02T15:04:05Z"} {
			if t, err := time.Parse(layout, x); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func formatValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case int, int32, int64:
		return fmt.Sprintf("%d", x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return fmt.Sprintf("%v", v)
}

func originalCall(n *ast.FuncNode) string {
	return "{" + n.Fn + "(" + strings.Join(n.Args, ", ") + ")}"
}

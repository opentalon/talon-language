package template

import (
	"testing"
	"time"

	"github.com/opentalon/talon-language/internal/ast"
)

func parsed(raw string) ast.Template {
	return ast.ParseTemplate(raw)
}

func TestRenderLiteral(t *testing.T) {
	got := Render(parsed("hello world"), RenderContext{})
	if got != "hello world" {
		t.Errorf("got %q", got)
	}
}

func TestRenderItemName(t *testing.T) {
	got := Render(parsed("{item.name}: {attr.km} km"), RenderContext{
		Row: Row{"name": "VW Transporter", "km": 45000.0},
	})
	if got != "VW Transporter: 45000 km" {
		t.Errorf("got %q", got)
	}
}

func TestRenderContextRef(t *testing.T) {
	got := Render(parsed("welcome {context.role}"), RenderContext{
		Context: map[string]any{"role": "manager"},
	})
	if got != "welcome manager" {
		t.Errorf("got %q", got)
	}
}

func TestRenderBareRef(t *testing.T) {
	got := Render(parsed("status: {status}"), RenderContext{
		Row: Row{"status": "active"},
	})
	if got != "status: active" {
		t.Errorf("got %q", got)
	}
}

func TestRenderCount(t *testing.T) {
	got := Render(parsed("{count} items overdue"), RenderContext{
		AggregateRows: []Row{{}, {}, {}},
	})
	if got != "3 items overdue" {
		t.Errorf("got %q", got)
	}
}

func TestRenderTotal(t *testing.T) {
	got := Render(parsed("total km: {total(attr.km)}"), RenderContext{
		AggregateRows: []Row{
			{"km": 100.0},
			{"km": 200.0},
			{"km": 300.0},
		},
	})
	if got != "total km: 600" {
		t.Errorf("got %q", got)
	}
}

func TestRenderAvg(t *testing.T) {
	got := Render(parsed("avg km: {avg(attr.km)}"), RenderContext{
		AggregateRows: []Row{
			{"km": 10.0},
			{"km": 20.0},
			{"km": 30.0},
		},
	})
	if got != "avg km: 20" {
		t.Errorf("got %q", got)
	}
}

func TestRenderMinMax(t *testing.T) {
	rows := []Row{{"price": 50.0}, {"price": 10.0}, {"price": 30.0}}
	if got := Render(parsed("{min(attr.price)} / {max(attr.price)}"), RenderContext{AggregateRows: rows}); got != "10 / 50" {
		t.Errorf("got %q", got)
	}
}

func TestRenderDaysUntil(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	got := Render(parsed("contract ends in {days_until(attr.expires_at)} days"), RenderContext{
		Row: Row{"expires_at": "2026-06-08"},
		Now: now,
	})
	if got != "contract ends in 7 days" {
		t.Errorf("got %q", got)
	}
}

func TestRenderDaysSince(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	got := Render(parsed("invoiced {days_since(attr.invoiced_at)} days ago"), RenderContext{
		Row: Row{"invoiced_at": "2026-05-25"},
		Now: now,
	})
	if got != "invoiced 7 days ago" {
		t.Errorf("got %q", got)
	}
}

func TestRenderUnresolvedRefLeavesPlaceholder(t *testing.T) {
	// "{attr.unknown}" — no such attribute on the row.
	got := Render(parsed("missing: {attr.unknown}"), RenderContext{Row: Row{}})
	if got != "missing: {attr.unknown}" {
		t.Errorf("got %q", got)
	}
}

func TestRenderUnknownFunctionLeavesPlaceholder(t *testing.T) {
	got := Render(parsed("nope: {wat(attr.km)}"), RenderContext{})
	if got != "nope: {wat(attr.km)}" {
		t.Errorf("got %q", got)
	}
}

func TestRenderHandcraftedTemplate(t *testing.T) {
	// Template with Raw but no Nodes — falls back to raw output.
	got := Render(ast.Template{Raw: "no interpolation"}, RenderContext{})
	if got != "no interpolation" {
		t.Errorf("got %q", got)
	}
}

// ─── ParseTemplate ───────────────────────────────────────────────────────────

func TestParseTemplateLiteralOnly(t *testing.T) {
	tmpl := ast.ParseTemplate("just text")
	if len(tmpl.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(tmpl.Nodes))
	}
	lit, ok := tmpl.Nodes[0].(*ast.LiteralNode)
	if !ok || lit.Text != "just text" {
		t.Errorf("got %+v", tmpl.Nodes[0])
	}
}

func TestParseTemplateRefAndFunc(t *testing.T) {
	tmpl := ast.ParseTemplate("hello {item.name}, total {total(attr.km)} km")
	// [Lit("hello "), Ref(item.name), Lit(", total "), Func(total, [attr.km]), Lit(" km")]
	if len(tmpl.Nodes) != 5 {
		t.Fatalf("expected 5 nodes, got %d (%+v)", len(tmpl.Nodes), tmpl.Nodes)
	}
	if _, ok := tmpl.Nodes[1].(*ast.RefNode); !ok {
		t.Errorf("node[1] should be RefNode, got %T", tmpl.Nodes[1])
	}
	fn, ok := tmpl.Nodes[3].(*ast.FuncNode)
	if !ok {
		t.Fatalf("node[3] should be FuncNode, got %T", tmpl.Nodes[3])
	}
	if fn.Fn != "total" || len(fn.Args) != 1 || fn.Args[0] != "attr.km" {
		t.Errorf("FuncNode: %+v", fn)
	}
}

func TestParseTemplateBareCountIsFunc(t *testing.T) {
	tmpl := ast.ParseTemplate("{count} items")
	fn, ok := tmpl.Nodes[0].(*ast.FuncNode)
	if !ok || fn.Fn != "count" || len(fn.Args) != 0 {
		t.Errorf("expected zero-arg FuncNode{count}, got %+v", tmpl.Nodes[0])
	}
}

func TestParseTemplateUnterminated(t *testing.T) {
	tmpl := ast.ParseTemplate("hello {item.name unterminated")
	// Should not panic, should produce something.
	if len(tmpl.Nodes) == 0 {
		t.Error("expected at least one node")
	}
}

package reactive

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/factstore"
	talonlog "github.com/opentalon/talon-language/internal/log"
)

// LoggingActionHandler returns an ActionHandler that executes the actions
// inside the matched OnBlock's body. The minimum it knows how to do today
// is LoggerAction: the message template is interpolated against the
// triggering event and written through the internal/log package.
//
// BlockRefAction (`recommend "Name"` / `detect "Name"` references) is
// observed and logged at debug level — actually re-running the named
// block needs the full planner+executor pipeline, which is a layering
// the reactive package deliberately doesn't pull in.
//
// Use this as the default handler when wiring up a Dispatcher unless you
// have a richer integration:
//
//	d := reactive.New(reactive.LoggingActionHandler())
func LoggingActionHandler() ActionHandler {
	return func(ctx context.Context, block *ast.OnBlock, ev factstore.Event) {
		for _, action := range block.Actions {
			switch a := action.(type) {
			case *ast.LoggerAction:
				dispatchLoggerAction(ctx, block, ev, a)
			case *ast.BlockRefAction:
				dispatchBlockRef(ctx, block, ev, a)
			}
		}
	}
}

func dispatchLoggerAction(ctx context.Context, block *ast.OnBlock, ev factstore.Event, a *ast.LoggerAction) {
	msg := interpolateTemplate(a.Message.Raw, ev)
	logger := talonlog.Default().With(
		"source", "on_block",
		"trigger", block.Trigger,
		"block", block.Name,
	)
	switch strings.ToLower(a.Level) {
	case "warn", "warning":
		logger.WarnContext(ctx, msg)
	case "error":
		logger.ErrorContext(ctx, msg)
	default:
		logger.InfoContext(ctx, msg)
	}
}

func dispatchBlockRef(ctx context.Context, block *ast.OnBlock, ev factstore.Event, a *ast.BlockRefAction) {
	talonlog.Default().DebugContext(ctx, "on_block_ref",
		"source", "on_block",
		"trigger", block.Trigger,
		"block", block.Name,
		"ref_kind", a.Kind,
		"ref_name", a.Name,
		"fact_attr", ev.Fact.Attribute,
	)
}

// interpolateTemplate replaces a small set of placeholders in the
// message template:
//
//	{event.attr}     — the attribute name on the triggering fact
//	{event.value}    — the new value
//	{event.prev}     — the previous value (for `on change`)
//	{event.entity}   — the entity ID
//
// Anything else is left as-is. This is intentionally narrow: the test
// runner / executor have a richer renderer for `{item.name}` etc.; the
// reactive handler only sees one event at a time and shouldn't reinvent
// that pipeline.
func interpolateTemplate(raw string, ev factstore.Event) string {
	r := strings.NewReplacer(
		"{event.attr}", ev.Fact.Attribute,
		"{event.value}", formatValue(ev.Fact.Value),
		"{event.prev}", formatValue(ev.Prev.Value),
		"{event.entity}", ev.Fact.Entity,
	)
	return r.Replace(raw)
}

func formatValue(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

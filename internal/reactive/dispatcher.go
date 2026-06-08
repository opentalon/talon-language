// Package reactive routes FactStore events to matching `on` blocks. See
// docs/reactive.md and issue #23.
//
// The dispatcher is intentionally minimal: it filters events by trigger kind
// and matches OnBlock guards (attribute name for `on change`, fact type for
// `on assert`/`on retract`). When an OnBlock matches an event, the registered
// ActionHandler is invoked with the block and the event. Evaluating the
// block's `when` predicate and dispatching its body actions belong to the
// executor — they need access to the condition evaluator and to the rest of
// the rule pipeline, neither of which this package should know about.
package reactive

import (
	"context"
	"sync"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/factstore"
)

// ActionHandler receives an OnBlock that matched an event. It is responsible
// for evaluating the block's `when` condition (if any) and running its body.
type ActionHandler func(ctx context.Context, block *ast.OnBlock, ev factstore.Event)

// Dispatcher routes events from a FactStore to registered OnBlocks. It is
// safe for concurrent use.
type Dispatcher struct {
	mu      sync.RWMutex
	blocks  []*ast.OnBlock
	handler ActionHandler
}

// New constructs a dispatcher. The handler is invoked for every (block, event)
// pair that matches. Passing nil leaves the handler unset; you can configure
// it later with SetHandler before subscribing.
func New(handler ActionHandler) *Dispatcher {
	return &Dispatcher{handler: handler}
}

// SetHandler replaces the action handler. Safe to call at any time.
func (d *Dispatcher) SetHandler(h ActionHandler) {
	d.mu.Lock()
	d.handler = h
	d.mu.Unlock()
}

// Register adds an OnBlock to the dispatcher.
func (d *Dispatcher) Register(b *ast.OnBlock) {
	d.mu.Lock()
	d.blocks = append(d.blocks, b)
	d.mu.Unlock()
}

// Subscribe wires the dispatcher to a FactStore's EventEmitter. Returns an
// unsubscribe function. Pass the emitter the store exposes — typically by
// embedding factstore.EventEmitter.
func (d *Dispatcher) Subscribe(emitter *factstore.EventEmitter) (unsubscribe func()) {
	return emitter.Subscribe(d.handle)
}

// handle is the subscriber the dispatcher installs on the FactStore.
func (d *Dispatcher) handle(ctx context.Context, ev factstore.Event) {
	d.mu.RLock()
	blocks := make([]*ast.OnBlock, len(d.blocks))
	copy(blocks, d.blocks)
	handler := d.handler
	d.mu.RUnlock()
	if handler == nil {
		return
	}
	for _, b := range blocks {
		if matches(b, ev) {
			handler(ctx, b, ev)
		}
	}
}

// matches reports whether the OnBlock's trigger description applies to the
// given event. The block's `when` condition is intentionally not evaluated
// here — the action handler runs the language-level evaluator with full
// context.
func matches(b *ast.OnBlock, ev factstore.Event) bool {
	switch b.Trigger {
	case "change":
		if ev.Kind != factstore.EventChange {
			return false
		}
		return b.Attr == "" || b.Attr == ev.Fact.Attribute
	case "assert":
		if ev.Kind != factstore.EventAssert {
			return false
		}
		return b.FactType == "" || b.FactType == factTypeOf(ev.Fact)
	case "retract":
		if ev.Kind != factstore.EventRetract {
			return false
		}
		return b.FactType == "" || b.FactType == factTypeOf(ev.Fact)
	}
	return false
}

// factTypeOf extracts the type identifier from a Fact. Today the convention is
// that an attribute named "type" carries the fact type (e.g. "item",
// "activity"). When the FactStore grows a first-class type field, this helper
// is the single place that needs updating.
func factTypeOf(f factstore.Fact) string {
	if f.Attribute == "type" {
		if s, ok := f.Value.(string); ok {
			return s
		}
	}
	return ""
}

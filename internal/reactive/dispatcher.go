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

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/factstore"
)

// ActionHandler receives an OnBlock that matched an event. It is responsible
// for evaluating the block's `when` condition (if any) and running its body.
type ActionHandler func(ctx context.Context, block *ast.OnBlock, ev factstore.Event)

// Dispatcher routes events from a FactStore to registered OnBlocks. It is
// safe for concurrent use.
//
// Internally blocks are indexed by (EventKind, anchor) where the anchor is
// OnBlock.Attr for `on change` and OnBlock.FactType for `on assert` /
// `on retract`. Lookup per event is O(1) on the specific anchor plus a
// small wildcard slice (blocks with empty Attr/FactType — they fire on
// every event of the matching kind). This replaces the original linear
// scan that re-evaluated every registered block per event — the cost
// model that opentalon/talon-db#21 was filed to fix.
type Dispatcher struct {
	mu sync.RWMutex
	// byKind[kind][anchor] → matching blocks. Anchor is OnBlock.Attr for
	// change events, OnBlock.FactType for assert/retract events. The
	// empty-string key holds the wildcards for that kind (blocks that
	// match every event of the kind).
	byKind  map[factstore.EventKind]map[string][]*ast.OnBlock
	handler ActionHandler
}

// New constructs a dispatcher. The handler is invoked for every (block, event)
// pair that matches. Passing nil leaves the handler unset; you can configure
// it later with SetHandler before subscribing.
func New(handler ActionHandler) *Dispatcher {
	return &Dispatcher{
		handler: handler,
		byKind:  map[factstore.EventKind]map[string][]*ast.OnBlock{},
	}
}

// SetHandler replaces the action handler. Safe to call at any time.
func (d *Dispatcher) SetHandler(h ActionHandler) {
	d.mu.Lock()
	d.handler = h
	d.mu.Unlock()
}

// Register adds an OnBlock to the dispatcher's index. The block's
// trigger string determines which event kind it subscribes to; its
// Attr (for change) or FactType (for assert/retract) becomes the
// anchor key. An empty anchor lands the block in the wildcard slot
// for its kind.
func (d *Dispatcher) Register(b *ast.OnBlock) {
	kind, anchor, ok := indexKey(b)
	if !ok {
		return // unknown trigger; quietly ignore (matches old behaviour)
	}
	d.mu.Lock()
	bucket, exists := d.byKind[kind]
	if !exists {
		bucket = map[string][]*ast.OnBlock{}
		d.byKind[kind] = bucket
	}
	bucket[anchor] = append(bucket[anchor], b)
	d.mu.Unlock()
}

// indexKey extracts (eventKind, anchor) for an OnBlock. The anchor is
// OnBlock.Attr for change-triggered blocks and OnBlock.FactType for
// assert/retract-triggered blocks. An unknown trigger returns ok=false.
func indexKey(b *ast.OnBlock) (factstore.EventKind, string, bool) {
	switch b.Trigger {
	case "change":
		return factstore.EventChange, b.Attr, true
	case "assert":
		return factstore.EventAssert, b.FactType, true
	case "retract":
		return factstore.EventRetract, b.FactType, true
	}
	return 0, "", false
}

// Subscribe wires the dispatcher to a FactStore's EventEmitter. Returns an
// unsubscribe function. Pass the emitter the store exposes — typically by
// embedding factstore.EventEmitter.
func (d *Dispatcher) Subscribe(emitter *factstore.EventEmitter) (unsubscribe func()) {
	return emitter.Subscribe(d.handle)
}

// handle is the subscriber the dispatcher installs on the FactStore.
// It looks up matching blocks via the (kind, anchor) index in O(1) plus
// a small wildcard slice, rather than scanning every registered block.
func (d *Dispatcher) handle(ctx context.Context, ev factstore.Event) {
	d.mu.RLock()
	bucket := d.byKind[ev.Kind]
	handler := d.handler
	if handler == nil || bucket == nil {
		d.mu.RUnlock()
		return
	}
	anchor := eventAnchor(ev)
	// Snapshot the two slices we need so we can release the lock
	// before invoking handlers (they may re-enter the dispatcher).
	specific := append([]*ast.OnBlock(nil), bucket[anchor]...)
	var wildcards []*ast.OnBlock
	if anchor != "" {
		wildcards = append([]*ast.OnBlock(nil), bucket[""]...)
	}
	d.mu.RUnlock()
	for _, b := range specific {
		handler(ctx, b, ev)
	}
	for _, b := range wildcards {
		handler(ctx, b, ev)
	}
}

// eventAnchor extracts the per-kind index key from an event:
// the attribute for change events, the fact type for assert/retract.
func eventAnchor(ev factstore.Event) string {
	switch ev.Kind {
	case factstore.EventChange:
		return ev.Fact.Attribute
	case factstore.EventAssert, factstore.EventRetract:
		return factTypeOf(ev.Fact)
	}
	return ""
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

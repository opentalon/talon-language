package tln

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/executor"
	"github.com/opentalon/tln-language/internal/factstore"
	tlnlog "github.com/opentalon/tln-language/internal/log"
	"github.com/opentalon/tln-language/internal/planner"
	"github.com/opentalon/tln-language/internal/reactive"
)

// Firing records one on-block match and what it executed. A match with a
// logger-only body (no block reference) produces a Firing with an empty
// Ref/RefKind and a nil Result; a `workflow`/`detect`/`recommend`
// reference produces one Firing per reference carrying its Result (or
// Err).
type Firing struct {
	OnBlock string // the matched on-block's name, e.g. `on change attr "current_stock"`
	Ref     string // referenced block name ("" for logger-only bodies)
	RefKind string // "workflow" | "detect" | "recommend" | ""
	Event   Event
	Result  *Result // execution result of the referenced block (nil if none)
	Err     error   // non-nil if evaluating the guard or running the ref failed
}

// Session is a long-lived, event-driven tln runtime. It holds a
// compiled program and a MemoryStore, registers the program's on-blocks
// on a reactive dispatcher, and fires their referenced blocks when facts
// pushed in via [Session.Assert] / [Session.Retract] match.
//
// A Session is the embeddable core of a watcher agent: poll or webhook
// code maps external data into facts, Asserts them, and acts on the
// returned firings. Assert/Retract/RunAll are serialized — one mutation
// is processed at a time — so firings from a single call are complete
// and self-contained.
type Session struct {
	mu      sync.Mutex
	store   *factstore.MemoryStore
	plans   map[string]*planner.QueryPlan
	disp    *reactive.Dispatcher
	unsub   func()
	exec    *executor.Executor
	pending []Firing // accumulator for the in-flight Assert/Retract call
}

// NewSession compiles src and returns a Session ready to receive facts.
// It registers every on-block in the program on a reactive dispatcher
// subscribed to the session's MemoryStore.
//
// Options:
//   - [WithToolResolver] wires the caller used by mcp steps in fired workflows.
//   - [WithFactStore] supplies a pre-hydrated *MemoryStore (from a prior
//     [Session.Snapshot]); it must already hold its facts before this
//     call so that replaying them fires nothing — the dispatcher only
//     subscribes here, after hydration. Without it, a fresh empty store
//     is used.
//   - [WithFilename] labels diagnostics.
//
// The `when` clause on an on-block is restricted in this version to
// comparisons of the event fields new_value / prev_value / entity
// against literals, optionally combined with and/or. Any other shape
// (notably cross-fact lookups like `attr "minimum_amount"`) is rejected
// here with a *CompileError so an authoring caller learns immediately.
func NewSession(src string, opts ...Option) (*Session, error) {
	cfg := &runConfig{file: "<session>"}
	for _, opt := range opts {
		opt(cfg)
	}

	prog, plans, err := compileProgram(cfg.file, src)
	if err != nil {
		return nil, err
	}

	// Collect on-blocks and validate their guards up front.
	var onBlocks []*ast.OnBlock
	for _, b := range prog.Blocks {
		if on, ok := b.(*ast.OnBlock); ok {
			if verr := validateWhen(on); verr != nil {
				return nil, &CompileError{Stage: "validate", Diags: []Diagnostic{{
					Severity: SeverityError,
					Message:  fmt.Sprintf("%s: %s", on.Name, verr),
				}}}
			}
			onBlocks = append(onBlocks, on)
		}
	}

	var store *factstore.MemoryStore
	if cfg.factStore != nil {
		ms, ok := cfg.factStore.(*factstore.MemoryStore)
		if !ok {
			return nil, fmt.Errorf("tln: NewSession requires a *MemoryStore (from NewMemoryStore); got %T", cfg.factStore)
		}
		store = ms
	} else {
		store = factstore.NewMemoryStore()
	}

	exec := executor.NewExecutor(store)
	exec.Tools = cfg.mcp
	exec.ConfirmHook = cfg.confirm
	exec.ApprovalHook = cfg.approval
	exec.Queue = cfg.queue

	s := &Session{
		store: store,
		plans: plans,
		exec:  exec,
	}
	s.disp = reactive.New(s.handle)
	for _, on := range onBlocks {
		s.disp.Register(on)
	}
	// Subscribe last — after the store is fully hydrated — so replayed
	// snapshot facts do not fire on-blocks.
	s.unsub = s.disp.Subscribe(store.Events())
	return s, nil
}

// Assert pushes facts into the session's store. Matching on-blocks fire
// synchronously during the call; the returned slice records every
// firing, in dispatch order. An unchanged re-assert emits no events and
// therefore produces no firings.
func (s *Session) Assert(ctx context.Context, facts []Fact) ([]Firing, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = nil
	if err := s.store.Assert(ctx, facts); err != nil {
		return s.pending, err
	}
	return s.pending, nil
}

// Retract removes facts matching the pattern. Any `on retract` blocks
// fire synchronously; the returned slice records their firings.
func (s *Session) Retract(ctx context.Context, p RetractPattern) ([]Firing, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = nil
	if err := s.store.Retract(ctx, p); err != nil {
		return s.pending, err
	}
	return s.pending, nil
}

// RunAll executes every block in the program against the session store,
// independent of any event — the path a manual or scheduled trigger
// takes. Returns the aggregate result keyed by block name.
func (s *Session) RunAll(ctx context.Context) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	blocks, err := s.exec.RunAll(ctx, s.plans)
	if err != nil {
		return nil, err
	}
	return &Result{Blocks: blocks, Actions: collectActions(blocks)}, nil
}

// Snapshot returns the current store contents (entity id → attribute →
// value) for persistence. Feed it back by asserting these facts into a
// fresh MemoryStore before passing it to [NewSession] via
// [WithFactStore], which reproduces the state without re-firing.
func (s *Session) Snapshot() map[int]map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Snapshot()
}

// Close unsubscribes the dispatcher from the store. After Close the
// session no longer reacts to mutations; the store itself remains usable.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unsub != nil {
		s.unsub()
		s.unsub = nil
	}
}

// handle is the dispatcher's ActionHandler. It runs on the same
// goroutine as the Assert/Retract that emitted the event (MemoryStore
// dispatches synchronously), so appending to s.pending needs no
// additional lock — the caller already holds s.mu.
func (s *Session) handle(ctx context.Context, block *ast.OnBlock, ev factstore.Event) {
	// `on change ... to X`: the dispatcher matches on attribute only, so
	// enforce the target-value equality here.
	if block.ToValue != nil {
		want, ok := literalOf(block.ToValue)
		if !ok || !valuesEqual(ev.Fact.Value, want) {
			return
		}
	}

	if block.When != nil {
		pass, err := evalWhen(block.When, ev)
		if err != nil {
			s.pending = append(s.pending, Firing{OnBlock: block.Name, Event: ev, Err: err})
			return
		}
		if !pass {
			return
		}
	}

	// step("trigger").result.<field> resolves as vars["trigger_result"]
	// then navigates ".result.<field>", so the fields nest under "result"
	// — the same shape a real mcp step result has.
	presets := map[string]any{
		"trigger_result": map[string]any{
			"result": map[string]any{
				"entity": ev.Fact.RecordID,
				"attr":   ev.Fact.Attribute,
				"value":  ev.Fact.Value,
				"prev":   ev.Prev.Value,
				"kind":   ev.Kind.String(),
			},
		},
	}

	// Run the body in order: logger actions write through the log
	// package; block references run their plan and record a Firing.
	firedRef := false
	for _, a := range block.Actions {
		switch act := a.(type) {
		case *ast.LoggerAction:
			logOnAction(ctx, block, ev, act)
		case *ast.BlockRefAction:
			firedRef = true
			f := Firing{OnBlock: block.Name, Ref: act.Name, RefKind: act.Kind, Event: ev}
			plan, ok := s.plans[act.Name]
			if !ok {
				f.Err = fmt.Errorf("referenced block %q not found", act.Name)
				s.pending = append(s.pending, f)
				continue
			}
			var res *executor.BlockResult
			var err error
			if act.Kind == "workflow" {
				res, err = s.exec.RunWithPresets(ctx, plan, presets)
			} else {
				res, err = s.exec.Run(ctx, plan)
			}
			if err != nil {
				f.Err = err
			} else {
				blocks := map[string]*BlockResult{res.BlockName: res}
				f.Result = &Result{Blocks: blocks, Actions: collectActions(blocks)}
			}
			s.pending = append(s.pending, f)
		}
	}

	// A logger-only body (no block reference) still records that the
	// block matched, as a Firing with an empty Ref.
	if !firedRef {
		s.pending = append(s.pending, Firing{OnBlock: block.Name, Event: ev})
	}
}

// logOnAction interpolates an on-block logger action's template against
// the triggering event and writes it through the log package at the
// action's level. Recognized placeholders: {event.attr}, {event.value},
// {event.prev}, {event.entity}.
func logOnAction(ctx context.Context, block *ast.OnBlock, ev factstore.Event, a *ast.LoggerAction) {
	msg := strings.NewReplacer(
		"{event.attr}", ev.Fact.Attribute,
		"{event.value}", fmt.Sprintf("%v", ev.Fact.Value),
		"{event.prev}", fmt.Sprintf("%v", ev.Prev.Value),
		"{event.entity}", ev.Fact.RecordID,
	).Replace(a.Message.Raw)

	logger := tlnlog.Default().With("source", "on_block", "trigger", block.Trigger, "block", block.Name)
	switch strings.ToLower(a.Level) {
	case "warn", "warning":
		logger.WarnContext(ctx, msg)
	case "error":
		logger.ErrorContext(ctx, msg)
	default:
		logger.InfoContext(ctx, msg)
	}
}

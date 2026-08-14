package tln

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/opentalon/tln-language/internal/diagnostic"
	"github.com/opentalon/tln-language/internal/executor"
	"github.com/opentalon/tln-language/internal/lexer"
	"github.com/opentalon/tln-language/internal/parser"
	"github.com/opentalon/tln-language/internal/planner"
	"github.com/opentalon/tln-language/internal/validator"
)

// MCPCaller is the host callback tln uses to dispatch MCP tool calls
// produced by a workflow's mcp steps. Implementations route
// (server, tool, args) to whatever transport the host uses and return
// the structured result.
type MCPCaller = executor.MCPCaller

// ConfirmationHook runs before each MCP step. Returning false skips the
// step without invoking the MCPCaller; the step result is recorded as
// {"status":"skipped","reason":"confirmation_denied"}.
type ConfirmationHook = executor.ConfirmationHook

// ApprovalHook gates `remediate approve` MCP calls on a role-based
// decision. Install it with [WithApprovalHook].
type ApprovalHook = executor.ApprovalHook

// Queue receives `remediate queue` calls for later host-driven dispatch.
// Install it with [WithQueue]. QueuedCall carries one deferred call.
type Queue = executor.Queue
type QueuedCall = executor.QueuedCall

// BlockResult is the per-block execution outcome. Aliased here so
// consumers don't have to import internal/executor.
type BlockResult = executor.BlockResult

// StepResult records one step's execution inside a BlockResult.
type StepResult = executor.StepResult

// Action is one `do` clause a rule fired, resolved against one matched
// row: the verb, its resolved arguments, the id of the row it fired for,
// and the rule it came from. tln executes none of them — deciding which
// actions fire and what their arguments are is the engine's job, running
// them is the host's.
//
// An `attr` argument naming a fact the row does not carry is present and
// nil, not dropped: dropping it would silently shift the positional
// arguments a host reads.
type Action = executor.FiredAction

// Diagnostic is one compilation diagnostic (error, warning, or info).
type Diagnostic = diagnostic.Diagnostic

// Severity classifies a Diagnostic.
type Severity = diagnostic.Severity

// Diagnostic severity values.
const (
	SeverityError   = diagnostic.Error
	SeverityWarning = diagnostic.Warning
	SeverityInfo    = diagnostic.Info
)

// Result is the aggregate outcome of [RunWorkflow] or [Run]. Each
// entry in Blocks corresponds to one workflow / detect block in the
// source (keyed by block name — e.g. `workflow "Foo" { ... }` produces
// key "Foo").
type Result struct {
	Blocks map[string]*BlockResult

	// Actions is every action fired by every block in this run, after
	// defeasible resolution — a rule defeated for a row contributes
	// nothing. Ordered by block name, then by the block's own order
	// (rows in flagged-set order, a rule's `do` clauses in source
	// order), so the same facts and ruleset produce the same list every
	// time. Never nil; a ruleset with no `do` clauses yields an empty
	// slice.
	Actions []Action

	// ResolvedNames maps entity id → display name, populated only by
	// [Run] when a FactStore is wired up (the fact store holds the
	// `:attr/name` bindings). Empty for [RunWorkflow] results.
	ResolvedNames map[int]string
}

// CompileError reports failures from the lex / parse / validate / plan
// stages. Diags carries the full diagnostic list — including any
// non-error diagnostics surfaced alongside the failing ones.
type CompileError struct {
	Stage string // "lex" | "parse" | "validate" | "plan"
	Diags []Diagnostic
}

func (e *CompileError) Error() string {
	if len(e.Diags) == 0 {
		return fmt.Sprintf("tln: %s failed", e.Stage)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "tln: %s: %d diagnostic(s)", e.Stage, len(e.Diags))
	for _, d := range e.Diags {
		if d.Severity != diagnostic.Error {
			continue
		}
		b.WriteString("\n  ")
		b.WriteString(d.String())
	}
	return b.String()
}

// Option configures a [RunWorkflow] or [Run] invocation. The same
// Option type is shared between both entry points; inapplicable
// options (e.g. WithMCP on a Run with no MCP steps) are silently
// no-ops, matching the executor's tolerance today.
type Option func(*runConfig)

type runConfig struct {
	file      string
	mcp       MCPCaller
	confirm   ConfirmationHook
	approval  ApprovalHook
	queue     Queue
	factStore FactStore
}

// WithMCP installs the MCP caller. Required for workflows that contain
// MCP steps; without it, MCP steps return a stub {"status":"stub"}
// result and the host is never contacted.
func WithMCP(c MCPCaller) Option {
	return func(cfg *runConfig) { cfg.mcp = c }
}

// WithConfirmHook installs an optional per-step confirmation gate. The
// hook fires before each MCP call; returning false skips the call.
func WithConfirmHook(h ConfirmationHook) Option {
	return func(cfg *runConfig) { cfg.confirm = h }
}

// WithApprovalHook installs the gate for `remediate approve` calls. When
// absent, approve-mode calls are skipped (no approver).
func WithApprovalHook(h ApprovalHook) Option {
	return func(cfg *runConfig) { cfg.approval = h }
}

// WithQueue installs the sink for `remediate queue` calls. When absent,
// queue-mode calls are dropped (no queue).
func WithQueue(q Queue) Option {
	return func(cfg *runConfig) { cfg.queue = q }
}

// WithFilename labels diagnostics with this filename. Defaults to
// "<workflow>". Purely cosmetic — affects error messages only.
func WithFilename(name string) Option {
	return func(cfg *runConfig) { cfg.file = name }
}

// RunWorkflow compiles and executes a tln source string in
// workflow-only mode. The full pipeline runs:
//
//	lex → parse → validate → plan → execute
//
// Any errors during the first four stages return a *CompileError;
// runtime errors from MCP steps (or any other executor failure)
// surface as a plain error.
//
// MCP steps are dispatched via the MCPCaller installed by [WithMCP].
// Without an MCPCaller, MCP steps are stubbed — useful for compiling
// a workflow to inspect its plan without contacting the host.
//
// No Datalevin client is wired up here; programs that include
// FactQuery / MLComputation steps are not supported by this entry
// point and will fail at execution time. A future Run / RunFull entry
// point will accept a Datalevin client for the full language surface.
func RunWorkflow(ctx context.Context, src string, opts ...Option) (*Result, error) {
	cfg := &runConfig{file: "<workflow>"}
	for _, opt := range opts {
		opt(cfg)
	}

	tokens, lexDiags := lexer.Lex(cfg.file, src)
	if lexDiags.HasErrors() {
		return nil, &CompileError{Stage: "lex", Diags: lexDiags}
	}

	prog, parseDiags := parser.Parse(cfg.file, tokens)
	if parseDiags.HasErrors() {
		return nil, &CompileError{Stage: "parse", Diags: parseDiags}
	}

	if valDiags := validator.Validate(cfg.file, prog); valDiags.HasErrors() {
		return nil, &CompileError{Stage: "validate", Diags: valDiags}
	}

	plans, planDiags := planner.Plan(prog)
	if planDiags.HasErrors() {
		return nil, &CompileError{Stage: "plan", Diags: planDiags}
	}

	// Detect-style programs (anything that needs the fact store) must
	// go through Run, not RunWorkflow — calling on through here would
	// panic in the executor at the first Query call on a nil Client.
	// Surface a typed error so callers can distinguish "wrong entry
	// point" from a real compile / runtime failure.
	if needsFactStore(plans) {
		return nil, ErrRequiresFactStore
	}

	exec := &executor.Executor{
		MCP:          cfg.mcp,
		ConfirmHook:  cfg.confirm,
		ApprovalHook: cfg.approval,
		Queue:        cfg.queue,
	}
	blocks, err := exec.RunAll(ctx, plans)
	if err != nil {
		return nil, err
	}

	return &Result{Blocks: blocks, Actions: collectActions(blocks)}, nil
}

// collectActions flattens the per-block action lists into the run-level
// one. Blocks come out of the executor in a map, so we walk them in
// sorted name order: two runs over the same facts must produce a
// byte-identical action list for a host to build determinism claims on
// top of it.
func collectActions(blocks map[string]*BlockResult) []Action {
	names := make([]string, 0, len(blocks))
	for name := range blocks {
		names = append(names, name)
	}
	sort.Strings(names)
	out := []Action{}
	for _, name := range names {
		out = append(out, blocks[name].Actions...)
	}
	return out
}

// needsFactStore reports whether any compiled plan contains a step
// that the executor will dispatch against the FactStore (today: any
// FactQuery step). Used by RunWorkflow to reject programs that
// require a fact store rather than silently no-op'ing the steps.
func needsFactStore(plans map[string]*planner.QueryPlan) bool {
	for _, plan := range plans {
		for _, step := range plan.Steps {
			if _, ok := step.(*planner.FactQuery); ok {
				return true
			}
		}
	}
	return false
}

package talon

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentalon/talon-language/internal/datalevin"
	"github.com/opentalon/talon-language/internal/executor"
	"github.com/opentalon/talon-language/internal/factstore"
	"github.com/opentalon/talon-language/internal/imports"
	"github.com/opentalon/talon-language/internal/lexer"
	"github.com/opentalon/talon-language/internal/parser"
	"github.com/opentalon/talon-language/internal/planner"
	"github.com/opentalon/talon-language/internal/validator"
)

// FactStore is the storage backend [Run] uses to evaluate detect /
// query blocks and to seed facts via [Seed]. Aliased from the
// executor's internal interface so the two stay in lockstep.
//
// The shipped backend is Datalevin (internal/datalevin/client.go) — a
// *datalevin.Client satisfies this interface unchanged. A future SQL
// or vector-store backend can satisfy it too; method shapes reflect
// Datalog conventions (Query takes a query string, Transact takes
// fact maps), so a non-Datalog backend's impl is responsible for
// translating to its native dialect.
type FactStore = executor.FactStore

// ErrRequiresFactStore is returned by [RunWorkflow] when the program
// contains detect / query blocks that need a fact store. Switch to
// [Run] with [WithFactStore] (or [WithDatalevinURL] for the default
// backend) on the same source.
var ErrRequiresFactStore = errors.New("talon: program contains detect/query blocks; use Run with a FactStore")

// WithFactStore installs a FactStore for [Run] and [Seed]. Required
// for programs containing detect or query blocks; ignored otherwise.
func WithFactStore(s FactStore) Option {
	return func(cfg *runConfig) { cfg.factStore = s }
}

// WithDatalevinURL is sugar over [WithFactStore]: it constructs the
// default Datalevin HTTP client against the given URL and runs a
// Health check on dial so misconfigured deploys fail fast at Run
// time rather than at first Query.
//
// Tests and callers using a fake or alternate backend should pass
// [WithFactStore] directly instead.
func WithDatalevinURL(url string) Option {
	return func(cfg *runConfig) {
		cfg.factStore = NewFactStore(url)
	}
}

// NewFactStore returns a FactStore backed by the default shipped
// implementation (today: Datalevin over HTTP) connected to the given
// URL. The store runs a Health() check lazily on the first store
// access, so misconfigured deploys surface a clean error at
// Run / Seed time rather than panicking deep inside the executor —
// and construction never blocks on the backend being up.
//
// The function name is intentionally backend-neutral: when a SQL or
// vector-store-backed implementation lands, this constructor stays
// the same and dispatches on URL scheme (e.g. `datalevin://...`,
// `postgres://...`). Callers using a custom or fake FactStore should
// pass it to [WithFactStore] directly instead.
//
// NewFactStore is for long-lived backends — typically a plugin or
// service that holds the store across many Run / Seed calls. For
// one-shot use within a single Run, prefer [WithDatalevinURL].
func NewFactStore(url string) FactStore {
	return &healthCheckedClient{client: datalevin.NewClient(url), url: url}
}

// NewMemoryStore returns a fresh Prolog-style in-memory FactStore. It
// satisfies the same interface as the Datalevin client, so any code
// path that accepts a FactStore — Run, Seed, the executor — accepts a
// MemoryStore unchanged. Useful for tests, the REPL, and embedded
// deployments where a sidecar is overkill.
func NewMemoryStore() *factstore.MemoryStore {
	return factstore.NewMemoryStore()
}

// healthCheckedClient wraps a *datalevin.Client and runs Health()
// the first time the executor touches the store, so the URL-sugar
// path surfaces a clean error instead of panicking on the first
// query failure deep inside the executor.
type healthCheckedClient struct {
	client  *datalevin.Client
	url     string
	checked bool
}

func (h *healthCheckedClient) check(ctx context.Context) error {
	if h.checked {
		return nil
	}
	if err := h.client.Health(ctx); err != nil {
		return fmt.Errorf("talon: datalevin at %s unreachable: %w", h.url, err)
	}
	h.checked = true
	return nil
}

func (h *healthCheckedClient) Query(ctx context.Context, q factstore.Query) ([][]any, error) {
	if err := h.check(ctx); err != nil {
		return nil, err
	}
	return h.client.Query(ctx, q)
}

func (h *healthCheckedClient) Assert(ctx context.Context, facts []factstore.Fact) error {
	if err := h.check(ctx); err != nil {
		return err
	}
	return h.client.Assert(ctx, facts)
}

// Run compiles and executes a full Talon source (workflow + detect /
// query / ML primitives). Returns [ErrRequiresFactStore] when the
// program needs a fact store but none was wired up.
//
// Pipeline: lex → parse → validate → plan → execute.
// Compile-stage errors return *CompileError; runtime errors from
// MCP steps or the fact store surface as plain errors.
func Run(ctx context.Context, src string, opts ...Option) (*Result, error) {
	cfg := &runConfig{file: "<talon>"}
	for _, opt := range opts {
		opt(cfg)
	}

	plans, err := compile(cfg.file, src)
	if err != nil {
		return nil, err
	}

	if needsFactStore(plans) && cfg.factStore == nil {
		return nil, ErrRequiresFactStore
	}

	exec := executor.NewExecutor(cfg.factStore)
	exec.MCP = cfg.mcp
	exec.ConfirmHook = cfg.confirm

	blocks, err := exec.RunAll(ctx, plans)
	if err != nil {
		return nil, err
	}

	result := &Result{Blocks: blocks}
	if cfg.factStore != nil {
		// Best-effort name resolution: a fact store with no
		// :attr/name bindings returns an empty map and that's fine.
		// We don't surface a resolution error because a missing
		// names table is not a Run failure.
		if names, nerr := exec.ResolveNames(ctx, nil); nerr == nil {
			result.ResolvedNames = names
		}
	}

	return result, nil
}

// Seed parses a .talon.test source and pushes its facts into the
// given store. Returns the number of entities written.
//
// Separated from [Run] because the typical pattern is to seed once at
// startup and then Run programs many times against the same store.
// Takes src as a string (not an AST type) to keep internal packages
// out of the public surface.
func Seed(ctx context.Context, store FactStore, src string, opts ...Option) (int, error) {
	if store == nil {
		return 0, errors.New("talon: Seed requires a FactStore")
	}

	cfg := &runConfig{file: "<seed>"}
	for _, opt := range opts {
		opt(cfg)
	}

	tokens, lexDiags := lexer.Lex(cfg.file, src)
	if lexDiags.HasErrors() {
		return 0, &CompileError{Stage: "lex", Diags: lexDiags}
	}
	prog, parseDiags := parser.Parse(cfg.file, tokens)
	if parseDiags.HasErrors() {
		return 0, &CompileError{Stage: "parse", Diags: parseDiags}
	}

	exec := executor.NewExecutor(store)
	return exec.Seed(ctx, prog)
}

// compile runs lex → parse → resolve imports → validate → plan,
// returning a CompileError when any stage fails. Shared between
// Run and RunWorkflow.
//
// The `file` argument is used both as the diagnostic label and as the
// base path for relative import resolution. SDK callers that work
// from in-memory source (no on-disk path) should either avoid
// imports or pass absolute paths in their source.
func compile(file, src string) (map[string]*planner.QueryPlan, error) {
	tokens, lexDiags := lexer.Lex(file, src)
	if lexDiags.HasErrors() {
		return nil, &CompileError{Stage: "lex", Diags: lexDiags}
	}
	prog, parseDiags := parser.Parse(file, tokens)
	if parseDiags.HasErrors() {
		return nil, &CompileError{Stage: "parse", Diags: parseDiags}
	}
	if len(prog.Imports) > 0 {
		merged, importDiags := imports.Resolve(prog, file)
		if importDiags.HasErrors() {
			return nil, &CompileError{Stage: "imports", Diags: importDiags}
		}
		prog = merged
	}
	if valDiags := validator.Validate(file, prog); valDiags.HasErrors() {
		return nil, &CompileError{Stage: "validate", Diags: valDiags}
	}
	plans, planDiags := planner.Plan(prog)
	if planDiags.HasErrors() {
		return nil, &CompileError{Stage: "plan", Diags: planDiags}
	}
	return plans, nil
}

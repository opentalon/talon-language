# `github.com/opentalon/talon-language/pkg/talon`

Public Go SDK for the Talon language.

The `talon` CLI is the canonical embedding of the compile + execute pipeline. This package exposes that same pipeline to other Go programs so they can run Talon source against a host-supplied MCP caller without depending on `internal/` packages.

## Scope

Two entry points cover different parts of the language:

- **`RunWorkflow(ctx, src, opts...)`** — workflow-only programs (`workflow "..." { step "..." { mcp "..." "..." { ... } } }`). No fact-store dependency. Returns `ErrRequiresFactStore` if the program contains `detect` / query blocks.
- **`Run(ctx, src, opts...)`** — the full language: `detect`, queries, ML primitives, and workflows. Requires a `FactStore` wired via `WithFactStore(...)` or, for the default Datalevin backend, `WithDatalevinURL(...)`.
- **`Seed(ctx, store, src)`** — populate a `FactStore` from a `.talon.test` source. Typically called once at startup; `Run` is called many times against the same seeded store.

The `FactStore` interface is **backend-neutral**. The shipped implementation is Datalevin (`internal/datalevin.Client` satisfies it), but a future SQL- or vector-store-backed implementation can plug in without touching call sites.

## Usage

```go
import (
    "context"

    "github.com/opentalon/talon-language/pkg/talon"
)

type myCaller struct{}

func (m *myCaller) Call(ctx context.Context, server, tool string, args map[string]any) (any, error) {
    // route to your tool/MCP transport; return the structured result
    return map[string]any{"ok": true}, nil
}

func run(ctx context.Context, src string) error {
    result, err := talon.RunWorkflow(ctx, src,
        talon.WithMCP(&myCaller{}),
        talon.WithFilename("my_workflow.talon"),
    )
    if err != nil {
        return err
    }
    for name, block := range result.Blocks {
        // inspect block.Steps, block.Vars, etc.
        _ = name
        _ = block
    }
    return nil
}
```

### Full-language example with a fact store

```go
result, err := talon.Run(ctx, src,
    talon.WithDatalevinURL("http://localhost:8898"),
    talon.WithMCP(&myCaller{}),
)
```

Or with a custom backend / test fake:

```go
result, err := talon.Run(ctx, src, talon.WithFactStore(myStore))
```

### Options

| Option | Purpose |
|---|---|
| `WithMCP(MCPCaller)` | Required for programs containing MCP steps. Without it, MCP steps return `{"status":"stub"}` and the host is never contacted. |
| `WithConfirmHook(ConfirmationHook)` | Per-step gate. Return `false` to skip the step; the step result is recorded as `{"status":"skipped","reason":"confirmation_denied"}`. |
| `WithFilename(name)` | Labels diagnostics with this filename. Defaults to `"<workflow>"` for `RunWorkflow`, `"<talon>"` for `Run`, `"<seed>"` for `Seed`. |
| `WithFactStore(s FactStore)` | (`Run`/`Seed`) Installs a FactStore — required for programs with `detect` / query blocks. |
| `WithDatalevinURL(url)` | (`Run`/`Seed`) Sugar over `WithFactStore` — constructs the default Datalevin HTTP client and Health-checks it on first store access. |

### Fired actions

A rule's `do` clauses come back on the result as data — Talon decides which
actions fire and resolves their arguments; running them is yours:

```go
for _, a := range result.Actions {
    // a.EntityID, a.Rule, a.Verb, a.Args
    perform(a)
}
```

`result.Actions` is every block's actions; `result.Blocks[name].Actions` is one
block's. Both are always non-nil. Ordering is fixed (blocks by name, rows in
flagged order, `do` clauses in source order), so the same facts and ruleset
produce the same list every run. An `attr` argument the row does not carry is
present as `nil` rather than dropped, and a rule defeated by an `overrides` edge
for that row contributes nothing. See `docs/actions.md`.

### Errors

- Failures during the lex / parse / validate / plan stages return a `*CompileError`. The `Stage` field identifies which stage failed; `Diags` carries the full diagnostic list.
- `ErrRequiresFactStore` (sentinel) — `RunWorkflow` rejected a program containing `detect`/query blocks, or `Run` was called without a FactStore on a program that needs one. Switch entry points / wire up `WithFactStore`.
- Runtime failures from `MCPCaller.Call`, `FactStore.Query`, etc. surface as plain errors from the underlying executor.

## Out of scope

- ML primitive registry customisation — internal default is used.
- Test-runner / REPL / trace / explain helpers — those live in the `talon` CLI.

## Stability

Two entry points are now published. `RunWorkflow` semantics are locked from v0.1.0; `Run` / `Seed` / `FactStore` / `WithFactStore` / `WithDatalevinURL` / `ErrRequiresFactStore` are new in v0.2.0. Any breaking change gets a major-version bump.

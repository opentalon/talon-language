# `github.com/opentalon/talon-language/pkg/talon`

Public Go SDK for the Talon language.

The `talon` CLI is the canonical embedding of the compile + execute pipeline. This package exposes that same pipeline to other Go programs so they can run Talon source against a host-supplied MCP caller without depending on `internal/` packages.

## Scope

Today: **workflow-only programs** — those whose blocks consist of MCP step chains and pure Go computations (`workflow "..." { step "..." { mcp "..." "..." { ... } } }`).

Programs that contain Datalevin-backed queries or ML primitives need a live Datalevin client and are not yet supported by this SDK. A separate entry point for the full language surface will follow.

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

### Options

| Option | Purpose |
|---|---|
| `WithMCP(MCPCaller)` | Required for workflows containing MCP steps. Without it, MCP steps return `{"status":"stub"}` and the host is never contacted. |
| `WithConfirmHook(ConfirmationHook)` | Per-step gate. Return `false` to skip the step; the step result is recorded as `{"status":"skipped","reason":"confirmation_denied"}`. |
| `WithFilename(name)` | Labels diagnostics with this filename. Defaults to `"<workflow>"`. |

### Errors

- Failures during the lex / parse / validate / plan stages return a `*CompileError`. The `Stage` field identifies which stage failed; `Diags` carries the full diagnostic list.
- Failures during execution (e.g. an MCPCaller returning an error) return a plain `error` from the underlying executor.

## Out of scope

- Datalevin-backed execution
- ML primitives (`detect`, `forecast`, `cluster`, etc.)
- Test-runner / REPL / trace / explain helpers — those live in the `talon` CLI

## Stability

This package is the first published SDK surface. Symbols may evolve as more language features become reachable from outside; any breaking change will get a major-version bump.

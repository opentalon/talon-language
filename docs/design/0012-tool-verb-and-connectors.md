# ADR 0012 — `tool` verb, connectors, and env-resolved credentials

Status: accepted · the `tool` verb (replacing `mcp`) is implemented; `connector`
/ `env` grammar + two-mode routing are the accepted design and the tracked next
step (see "Status").

## Context

tln dispatched every external tool call with a hardcoded `mcp` keyword:

```tln
mcp "inventory" "get-list" { … }
```

But the runtime seam is protocol-agnostic — `ToolResolver.Call(server, tool,
args)`. `mcp` (Model Context Protocol) is just the *first plugin* that used it;
[io-tln](https://github.com/opentalon/io-tln) is another, [tln-mcp] a third.
Writing `mcp "io" "writeln"` to reach the I/O plugin is plainly wrong — the verb
names one plugin while addressing another.

Two things follow: the verb should be plugin-neutral, and the source should be
able to say *which plugin* backs a server name (and how to authenticate) so a
program can run with no Go host.

## Decision

### 1. `tool` replaces `mcp` as the only tool-call verb

`mcp` is removed as a keyword. Every call — including the test-DSL forms — uses
the neutral verb:

```tln
tool "io" "writeln"        { text "overdue: {item.id}" }   # → io plugin
tool "inventory" "get-list" { query "x" }                  # → mcp plugin
```

Mapping is unchanged underneath: `tool "S" "T" { … }` → `Call("S", "T", args)`.
Test DSL follows: `mock mcp` → `mock tool`, `mcp_called` → `tool_called`. (AST
type names retain `MCP*` internally; a rename is a mechanical follow-up.)

### 2. `connector` defines a server's plugin + config in source

```tln
connector "inventory" via mcp {
  endpoint env "INVENTORY_ENDPOINT"        # or a literal
  auth bearer env "INVENTORY_TOKEN"
}
connector "audit" via io { path "/var/log/tln/audit.log" }   # file sink
connector "io"    via io { }                                  # stdout (default)
```

`connector "name" via <plugin> { config }` binds a server name to a plugin and
its connection config. `io` takes `path` / `stream`; `mcp` takes `endpoint` /
`auth`.

### 3. Credentials (and endpoints) are `env`-resolved, never inlined

`env "VAR"` is a value resolved at run time from the environment (standalone) or
the host's env resolver (host mode). `.tln` files hold only the *names* — no
tokens, no secret-bearing URLs — so they stay commit-safe.

### 4. Two-mode resolution — host wins, source is the fallback

For each `tool "name" …`, the binding is resolved in order:

| Order | Source | Mode |
|-------|--------|------|
| 1 | host wired a resolver for `name` (Go `WithToolResolver`) | host present → host wins |
| 2 | a `connector "name"` block in the tln source | used when no host binding |
| 3 | built-in `io` default (stdout/stderr/stdin) | standalone only |
| 4 | none → compile/runtime error | — |

So `mcp` **always** needs defining (host-wired *or* a `connector` block, because
it needs an endpoint + creds), while `io` is a built-in default that applies in
the no-host case. A host owns its own I/O; `io` is not auto-provided under a
host.

### 5. Security — `env` and `io` are host-gated capabilities

External access is a **capability the host grants**, not something source can take:

- **`env` is deny-by-default and connector-scoped.** It parses *only* inside a
  connector's config — the general expression grammar rejects it — so an
  environment value can never flow into a `label`, a stored fact, or a `tool`
  argument. Even inside a connector, it resolves only if the host installs an
  env resolver; with none, `env` is unavailable.
- **Sandboxed / untrusted-source hosts deny more.** `tln-plugin` executes
  **LLM-authored** workflows, where `env` (secret exfiltration) and `io`
  (arbitrary host filesystem/stdio) are both too dangerous — a prompt-injected
  workflow could read secrets or write files. In that context the host **cuts
  `env` entirely and restricts the `io` server**, permitting only host-mediated
  `tool` calls that run through its own policy/credential path. Core exposes the
  capability set; the host decides it.

### 6. Metaprogramming — macros may emit `tool`, never `connector`

A compile-time macro ([ADR 0011](0011-compile-time-macros.md)) may emit **`tool`
calls** — e.g. splice `tool "audit" "writeln"` into every rule it generates. It
may **not** emit a **`connector`** (or an `env` value): the expansion phase
rejects any macro output containing a `ConnectorBlock`, and `env` is
unreachable from the macro grammar for the same reason it is unreachable from
the general expression grammar.

This is a security boundary. A connector grants external access with
credentials; letting macro-generated (and thus potentially injected) code mint
one would defeat the whole capability model. **Connectors are author-only** —
they must appear verbatim in the source a human wrote, never be conjured by
expansion.

## Consequences

- `mcp` is no longer privileged; adding a plugin needs no grammar change — the
  plugin is a server name, defined by a `connector` or wired by the host.
- A `.tln` program can declare its own external dependencies (mcp endpoints +
  env-based creds) and run with no Go host.
- Secrets never enter source or the AST.

## Status / next steps

- **Done:** `mcp` → `tool` verb. **`connector "name" via <plugin> { … }`** and
  connector-scoped **`env "VAR"`** — lexer, AST (`ConnectorBlock`, `EnvExpr`),
  parser (env rejected outside connectors), printer round-trip, validator,
  grammar, and tests. Full suite green.
- **Next PR (runtime):** two-mode routing — a plugin-factory registry
  (`WithPlugin(name, factory)`) so core wires `connector → plugin` without
  importing plugins, an env resolver the host installs (deny-by-default), and
  the host-wins / connector-fallback / built-in-`io` / error resolution order.
  io-tln reads `path` / `stream`.
- **Next PR (sandbox):** capability gating so `tln-plugin` cuts `env` and
  restricts the `io` server for LLM-authored source; and the macro-expansion
  phase (ADR 0011) rejecting any expansion output that contains a
  `ConnectorBlock` (connectors are author-only).

# ADR 0011 — Compile-time macros in tln core

Status: accepted · decision and the `macro.Expand` phase merged (#185); the phase
ships as an identity transform and the `defmacro`/`quote`/`unquote` grammar +
rewrite engine are the tracked follow-up (see "Status / next steps").

## Context

The Prolog-porting work ([tln-prolog](https://github.com/opentalon/tln-prolog))
kept surfacing the same wish: metaprogramming — "code that writes code," the way
Elixir uses `defmacro`/`quote`/`unquote`. The natural question was *why not in
core?*

Two kinds of metaprogramming hide under one word, and they land in opposite
places:

- **Runtime term-rewriting** (Prolog `term_expansion`, `=..`, `call/N` over
  compound terms) manipulates code-as-data *at runtime*. It needs function
  symbols, which tln core's `Term = Var | Lit` does not have. Putting it in core
  would turn core into a term-rewriting logic engine — i.e. into Prolog — and
  cost core its termination guarantee. That belongs in the tln-prolog engine.
- **Compile-time macros** (the Elixir model) expand *before* execution into
  ordinary code. The runtime never sees a macro or a term. This needs no runtime
  terms and preserves core's execution contract — and only core can add the
  grammar and a compile phase, so a plugin cannot provide it.

This ADR adopts the second.

## Decision

Add a **compile-time macro-expansion phase** to tln core. Macros expand to a
fixpoint into ordinary `ast` blocks; `validator.Validate` and `planner.Plan` —
and therefore the runtime resolver — consume the expanded program unchanged.

### Pipeline placement

The phase slots into `compileProgram` (pkg/tln/run.go) between import resolution
and validation:

```
lexer.Lex → parser.Parse → imports.Resolve → macro.Expand → validator.Validate → planner.Plan → run
                                              ▲ new phase (ADR 0011)
```

After imports (so imported macros are in scope) and before validation (so the
validator only ever sees ordinary, fully-expanded AST). Implemented as
`internal/macro.Expand(file, *ast.Program) (*ast.Program, diagnostic.List)`,
wired in now as the identity transform; the grammar and rewrite engine fill it
in.

### Surface (proposed)

- `quote { … }` — turn a block of tln source into a homoiconic **AST value**
  (code-as-data), the thing macros build and return.
- `unquote(x)` — splice a bound value into a `quote`d template.
- `defmacro name(params) { quote { … } }` — a compile-time function from
  arguments to AST. Invocations `name(args)` at top level are replaced by the
  AST the macro returns.

The quoted-AST value is a **compile-time-only** representation, distinct from the
runtime `Term = Var | Lit`. It is expanded away before planning, so the runtime
gains no compound terms.

### Termination

Expansion is the one place tln admits unbounded computation (a macro can emit
code that triggers another macro). It runs under a step budget
(`macro.MaxExpansionSteps`); exceeding it is a **compile-time** diagnostic
(`Stage: "macro"`), not a runtime hang. The runtime resolver stays exactly as
terminating as today because it only ever sees expanded, ordinary AST.

## Example

A macro that eliminates boilerplate across near-identical `detect` blocks:

```tln
defmacro over_threshold(name, metric, limit, prio) {
  quote {
    detect "High {unquote(name)}" {
      for records where type == "item" and attr unquote(metric) > unquote(limit)
      flag matching items
      label "{item.name}: high {unquote(name)}"
      priority unquote(prio)
    }
  }
}

over_threshold("temperature", "temp_c", 80, HIGH)
over_threshold("pressure",    "psi",   200, MEDIUM)
```

expands at compile time into two ordinary `detect` blocks — which is all the
validator, planner, and runtime ever see:

```tln
detect "High temperature" {
  for records where type == "item" and attr "temp_c" > 80
  flag matching items
  label "{item.name}: high temperature"
  priority HIGH
}
detect "High pressure" {
  for records where type == "item" and attr "psi" > 200
  flag matching items
  label "{item.name}: high pressure"
  priority MEDIUM
}
```

## Consequences

- Metaprogramming lands **in core**, correctly: it is a language + compile-phase
  feature only core can own, and it changes nothing about runtime semantics.
- The runtime keeps its deterministic, terminating, auditable contract — macros
  are gone before the resolver runs.
- Distinct from tln-prolog: that engine owns *runtime* term-rewriting for porting
  full Prolog; this ADR owns *compile-time* code generation for tln itself. They
  do not overlap.

## Status / next steps

The `internal/macro.Expand` phase is wired into the pipeline as the identity
transform (proving placement, suite green). Remaining, tracked separately:
grammar for `defmacro`/`quote`/`unquote`, the quoted-AST value type, the
fixpoint rewrite engine with the step budget, and validator awareness of macro
definitions.

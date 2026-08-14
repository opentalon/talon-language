# ADR 0008 — Answer Set Programming as an external plugin

Status: accepted · Closes [#171](https://github.com/opentalon/tln-language/issues/171)

## Context

tln has a relational rule surface (`derive`, recursive `factstore.Rule` +
`RuleCall`, and negation under **well-founded semantics** —
[docs/well-founded.md](../well-founded.md)). Well-founded gives a **unique
three-valued model** (true / false / undefined) in polynomial time, which is
what tln's core needs: one deterministic, auditable answer.

**Answer Set Programming (stable-model semantics)** is the natural next
expressiveness step — it is where a rule set has **zero or many** answer sets
(`p :- not q. q :- not p.` → two models), the classic win for combinatorial
search, planning, and configuration ("tln as a new Prolog/ASP front-end").

But stable models are a poor fit for **core**:

- **Non-deterministic** — 0..N models contradicts the "one auditable answer /
  show me the trace" contract.
- **NP-hard** — needs grounding + search, not the inline query planner /
  polynomial well-founded resolver.
- The search and any external solver are a **host-boundary** concern — the same
  place IO already lives (FactStore, ToolResolver).

## Decision

ASP is an **external plugin**, not a core feature — the third plugin shape after
tln-db (a *store*, `pkg/factstore.FactStore`) and tln-mcp (a *tool*,
`tln.ToolResolver`). tln-asp is a **solver**.

- **Repo:** `opentalon/tln-asp`, pure Go, depends one-way on tln-language.
- **Program representation:** the public `pkg/factstore` types — a rule set is
  `[]factstore.Rule` (bodies of `Pattern`/`Predicate`/`RuleCall`/`Negation`)
  plus `[]factstore.Fact` (the EDB). No new DSL; no ASP text.
- **Backend:** **pure Go**. Ground the rules (the same pattern as
  `internal/factstore/wellfounded.go`: `groundRule` / `enumGen` /
  range-restriction), then enumerate stable models via the **Gelfond-Lifschitz
  reduct** — for a candidate `M`, `P^M` drops rules with a negative literal in
  `M` and strips the rest; `M` is stable iff `leastModel(P^M) == M`. Correct,
  self-contained, no clingo / cgo / subprocess. Not clingo-scale; the `Solver`
  interface leaves room for a clingo backend later without an API change.
- **Result:** `[]AnswerSet`, each convertible back to `[]factstore.Fact` so a
  host asserts answers into a store or inspects them.

Core stays code-free and deterministic; the plugin owns the non-determinism and
the search.

## Core change

One SPI completion: `pkg/factstore` now re-exports **`Negation`** (it already
exports `Rule`/`RuleCall`/`Term`/`Pattern`), so an out-of-tree solver can build
`head :- body, not q` from the public surface alone. Proven by
`pkg/factstore/spi_test.go`.

## Boundary in use

```go
prog := tlnasp.Program{Rules: rules /* []factstore.Rule */, Facts: facts}
sets, _ := tlnasp.New().Solve(ctx, prog)   // 0..N answer sets
for _, s := range sets { store.Assert(ctx, s.Facts()) }
```

## Consequences / next step

Host-driven today: the host supplies the rule set (built from `pkg/factstore`
types). Solving the rules a `.tln` program *declares* (recursive/negated
`derive`) is the natural follow-up, but it depends on the tracked
"recursion / arity-N derive → `factstore.Rule`" work in
[docs/derive.md](../derive.md) plus a public API to export a program's compiled
`Query.Rules`. That is out of scope here and does not change this boundary.

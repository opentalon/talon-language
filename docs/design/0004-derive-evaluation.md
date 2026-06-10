# ADR-0004: `derive` block evaluation — lazy vs eager vs hybrid

## Status

Proposed. Depends on issue #91 (`derive` block) for the language
surface and #89 (RETE in talon-db) for the eager incremental path.
This ADR captures the evaluation-strategy design space so #91's
implementer doesn't have to relitigate it.

## Context

Issue #91 introduces `derive` blocks — patterns whose output is a new
fact in the FactStore, queryable by every other block exactly like an
asserted fact. The deductive cycle closes: cross-block chains become
writable in the language instead of in host glue.

A `derive` block needs an evaluation strategy. Two extremes:

1. **Lazy (top-down / backward chaining).** Don't compute anything
   until a downstream query references the derived predicate. When it
   does, resolve the rule's body by substituting bound args and
   recursing through the resolver. Cost is paid per query;
   intermediate state is zero.
2. **Eager (bottom-up / forward chaining).** Whenever the FactStore
   changes, fire the derive block's pattern, write the resulting
   tuples back into the store. Queries are then free — derived facts
   are real facts. Cost: write amplification and storage growth.

Real systems run both modes. Datalog literature calls the hybrid
**magic sets** (rewrite top-down goals into bottom-up rules with
sideways-information passing) or **demand-driven evaluation**. We
don't need to land magic sets to ship `derive`, but we do need to
pick a default and an annotation surface so callers can override.

## Current code that informs the choice

| Path | Relevance |
|---|---|
| `internal/factstore/rules.go` | Existing top-down resolver with memoization + cycle-break. Implements lazy mode for the existing `category_tree` recursive rule. `derive` reuses this verbatim. |
| `internal/factstore/factstore.go` (`Query.Rules`, `RuleCall`) | Lazy `derive` blocks compile straight to these — same wire shape as `category_tree`. No new types needed. |
| `internal/factstore/memory.go` (`MemoryStore.Assert/Retract`) | Insertion points for eager mode. Each call would fan to a list of registered derive blocks; matching tuples get re-asserted. |
| Issue #89 (RETE in talon-db) | The natural home for eager evaluation. Derived tuples become datoms that flow through the same RETE network as asserted ones — incremental update for free. |

## Decision

Ship `derive` blocks with **lazy evaluation as the default**, with an
explicit `eager` modifier that opts a block into pre-materialisation.

### Syntax

```talon
;; default — lazy, evaluated when referenced in a query
derive overdue(X) {
  for records where type == "vehicle"
    and attr "km" > attr "last_service_km" + 20000
}

;; explicit — pre-materialise; recompute on Assert/Retract
eager derive recall_candidates(X) {
  for records where type == "vehicle"
    and overdue(X)
    and attr "model" in ["Transit", "Sprinter"]
}
```

`eager` is a contextual keyword before `derive` (like `strict rule`
already is). Validator rejects `eager derive` for blocks whose body
references a `lazy` derive predicate where the reference chain has a
recursive cycle — the eager evaluator can't compute a fixpoint over
mixed strategies without magic sets, and that's out of v1 scope.

### Default rationale

- **Lazy is free.** The resolver in `rules.go` already exists. The
  query path's existing `Query.Rules` channel carries derive blocks
  with zero new infrastructure.
- **Eager pays for itself only when:**
  - The derived predicate is queried many times per Assert
    (amortises the materialisation cost across reads).
  - The predicate is small relative to the underlying facts (otherwise
    it bloats the store).
  - The host has an incremental engine — RETE (#89) — so updates
    don't trigger a full rescan on every Assert.
- **Talon's typical scale** (thousands of facts, dozens of blocks, ad
  hoc `Run`) makes lazy the right default. Eager becomes valuable
  when reactive on-blocks + RETE land; explicit opt-in keeps the
  language honest about the tradeoff.

### When to use `eager` (rule of thumb)

| Use `eager` when... | Use default (lazy) when... |
|---|---|
| Multiple blocks reference the same predicate in one Run | Single block references it |
| The predicate's pattern is expensive (joins across many entities) | Pattern is simple (one or two patterns) |
| Reactive on-blocks subscribe to changes in the predicate | Predicate is only queried in one-shot CLI/REPL flows |
| RETE backend (#89) is wired up | MemoryStore or Datalevin without RETE |

## Implementation phases

### Phase 1 — lazy only (lands with #91)

- Parser accepts `derive name(X) { selector }`; rejects `eager` modifier
  with "eager mode requires RETE backend; see #89".
- Planner compiles each `derive` block to a `factstore.Rule` entry.
- Any other block that references the predicate gets that rule
  appended to its `Query.Rules`.
- Evaluation runs through the existing `ruleCtx.resolve` resolver in
  `internal/factstore/rules.go` — already supports recursion, memo,
  cycle break.
- Validator: stratification check (no negation through recursion);
  cycle detection for non-recursive cycles.

### Phase 2 — eager mode (lands after #89)

- Lexer recognises `eager` as a `derive`-modifier keyword (contextual).
- Validator rejects `eager` on blocks whose body references lazy
  derive predicates inside a recursive cycle (stratification across
  modes is out of v1 scope).
- Planner emits a `MaterializeDerived` plan step for each eager block.
- Executor registers the block's pattern with the FactStore's
  `IncrementalStore` (defined in #89) and pipes Asserts/Retracts
  through. The resulting tuples are written back as real facts under
  a synthesised namespace (e.g. `:derived/overdue`).
- Reactive dispatcher subscribes to the materialised predicate's
  changes — `on assert derived/overdue { ... }` becomes natural.

### Phase 3 — magic sets (deferred)

If callers hit cases where:
- Lazy is too slow (predicate queried many times against unchanging
  facts), and
- Eager is too greedy (full materialisation would dwarf the store),

the rewrite phase moves to magic sets — top-down goals supplied at
query time become bottom-up rules with the goal's bound arguments
acting as a filter. This is a research-grade move; defer until
production workloads demand it.

## Interaction with other features

### RETE (#89)

Eager evaluation and RETE are designed to fit. The RETE network
treats derived facts as new datoms feeding back into the same
network — exactly how naive Datalog evaluation works, but
incremental. Concretely: an eager `derive` block compiles to a
RETE subnetwork whose terminal node, instead of firing a callback,
asserts a new datom that re-enters the network's alpha layer.

### Defeasible rules (`strict` / `overrides`)

`strict derive` and `overrides` on `derive` blocks are **rejected**.
`strict` and `overrides` are conflict-resolution between *rules that
act* (detect, rule, recommend); derived facts are *data*, not
actions. Two derive blocks with the same head simply union their
results — standard Datalog semantics.

### Multi-tenant (#15 / #4)

Derived facts inherit the tenant scope of the FactStore they're
written into. The language stays tenant-agnostic; the host wires the
FactStore (`Client.WithTenant`) before invoking `Run`. Lazy mode
naturally inherits this — the resolver only sees one tenant's facts.
Eager mode requires per-tenant materialisation registration; not a
problem in #89's design.

### Time-travel (when Datalevin ships `d/as-of`)

Lazy derive against an `AsOf` query is straightforward — the
resolver reads from the as-of view. Eager mode is more delicate —
do you maintain materialised views across history, or recompute
each as-of read? **Punt:** eager mode is current-state only in v1.

## Roadmap

| When | What |
|---|---|
| Issue #91 lands | Phase 1: lazy `derive` with stratification check |
| Issue #89 lands | Phase 2: `eager derive` modifier + RETE materialisation |
| If/when needed | Phase 3: magic sets / demand-driven for predicates where neither extreme fits |

## Verification

This ADR ships when:

1. Issue #91's implementation PR cites this doc in its design
   section.
2. The eager modifier is rejected with a clear error pointing at
   #89 — no surprises for early adopters who try `eager derive`
   before RETE exists.
3. The lazy path's behaviour matches the existing recursive resolver
   in `rules.go` — derive blocks should not introduce new evaluation
   semantics; they're a syntactic surface over the rule machinery
   that's already shipped.

## What this ADR does NOT decide

- Lazy resolver internals (already settled by `rules.go`).
- Eager materialisation storage layout — that's #89's call.
- Whether derived predicates can carry attributes (Alt B in #91) —
  syntactic question, orthogonal to evaluation mode.
- Probabilistic deduction (weighted derive blocks). Different
  research direction.

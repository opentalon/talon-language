# ADR-0003: Explainability — Tier 1

## Status

Tier 1 implemented (this PR). Tiers 2 and 3 deferred — see "Roadmap" below.

## Context

tln's pitch is deterministic, audit-first reasoning. The README promises:

> Detections are explainable and auditable.

Today, the explainability surface is mostly developer-facing: `tln
trace` emits JSON, and ML primitives carry an `Explanation` struct. A
procurement manager looking at a "*Order 100 bags of cement*"
recommendation cannot see *why* without help from an engineer.

This ADR scopes the user-facing layer. The minimum viable shape: when a
decision fires, surface (a) the rendered action, (b) the conditions
that satisfied the rule, (c) the observed fact values, (d) the chain
back through upstream blocks that triggered it.

## Decision

Ship three tiers, in priority order:

| Tier | Audience           | Surface                                    | Storage requirement     |
|------|--------------------|--------------------------------------------|-------------------------|
| 1    | End user (manager) | `ACTION`, `WHY`, `EVIDENCE` block, chain   | None — runtime only     |
| 2    | Auditor            | + fact IDs, observation times, source      | Datalevin tx-time       |
| 3    | Developer          | + raw JSON, decision IDs, replay           | talon-db (persistence)  |

This PR delivers **Tier 1**. The data needed is already in memory at
evaluation time — no FactStore changes required.

## Tier-1 Surface

### `internal/explain.Decision`

```go
type Decision struct {
    BlockName   string
    BlockKind   string         // "detect", "recommend", "forecast", …
    EntityID    int
    EntityName  string         // from :attr/name
    FiredAt     time.Time
    Action      string         // rendered label / suggest template
    Why         []string       // per-condition bullets
    Evidence    []Fact         // (attr, value, observed_at)
    TriggeredBy []Decision     // upstream chain
    Priority    string
    Confidence  string
}

type Fact struct {
    Attribute  string
    Value      any
    ObservedAt time.Time      // wall-clock at evaluation; see Tier 2
    Source     string         // best-effort; populated by ingest (Tier 2)
}
```

### `tln explain` command

```
tln explain <rules.tln> <tests.tln.test> [--test NAME] [--json]
```

Renders a Tier-1 view for every Decision produced by the test fixtures.
`--json` emits the structured form for downstream tooling.

Example output:

```
== Cement scenario ==
─────────────────────────────────────────────────────────────────
ACTION    Order Portland Cement 50kg — currently 12 bags, below minimum 50
ITEM      Portland Cement 50kg  (entity #808)
WHEN      Recommend 2026-05-27 19:59 UTC
PRIORITY  HIGH

WHY
  • current_stock 12 ≤ minimum_amount 50

EVIDENCE
  current_stock = 12
  minimum_amount = 50
─────────────────────────────────────────────────────────────────
```

### Cross-block chaining

When a `recommend.When` references `detect "X" matches`, the
recommend's `Decision.TriggeredBy` includes the matching detect's
`Decision`. `Render` walks `TriggeredBy` to merge upstream `Why`
bullets and dedupe `Evidence` across the chain.

This is computed entirely from the AST + in-memory entity set. No
storage state required.

### Template interpolation

The `label "..."` and `suggest "..."` templates support:

- `{item.name}` — entity's `:attr/name`
- `{attr.<name>}` — any `:attr/<name>` field
- `{<key>}` — best-effort lookup against `:record/<key>` then `:attr/<key>`

Unresolved placeholders are left in place verbatim so they're visible
in the rendered output, not silently dropped.

### Noise filtering

Two policies make the Tier-1 view readable:

1. **Why filtering.** Trivial selector predicates (`type == "stock_item"`,
   `status == "active"`, `category == "X"`) are dropped from `Why`
   bullets — they describe what kind of thing the rule selects, not
   why it fired.
2. **Evidence filtering.** `:record/type`, `:record/status`, and
   `:attr/name` are excluded from `Evidence` — already in the header.

## Non-goals (this PR)

- **Decision persistence.** Decisions live in memory for one
  evaluation. Re-querying historical decisions is a Tier-3 concern.
- **True fact observation times.** Datalevin has tx-times but
  `FactStore` doesn't expose them yet — `Fact.ObservedAt` is set to
  the wall-clock at evaluation, not the moment the fact was observed.
  Surfacing real observation times is the Tier-2 deliverable and a
  small amount of plumbing through the Datalevin client.
- **Counter-factuals.** "If `current_stock` were 51, this would not
  have fired." Requires re-evaluation against a forked fact store —
  Tier 3 and likely talon-db.
- **Confidence computation.** The `Confidence` field is present but
  not populated. Wiring it from ML primitives' confidence scores is
  follow-up work, not blocking the Tier-1 demo.
- **Recommend in `tln test` flagged assertions.** Recommend blocks
  don't populate the testrunner's `flagged` set today (their plan
  has no `DatalevinQuery` step). `Decisions` works around it via
  upstream chain walking, but `tln test`'s assertion checker
  doesn't — out of scope for Tier 1.

## Roadmap

- **Tier 2 (Datalevin extension).** Surface per-datom transaction time
  through the `FactStore` interface; populate `Fact.ObservedAt`
  truthfully; add `Fact.Source` from MCP ingest metadata. Adds an
  `--audit` flag to `tln explain` that emits the full chain with
  source attribution.
- **Tier 3 (talon-db).** Persist `Decision` records keyed by stable
  IDs; replay decisions against historical fact states; counter-factual
  queries. Forms the audit log for compliance use cases.

## Files

- `internal/explain/decision.go` — `Decision`, `Fact`, `Render`,
  `RenderAll`, chain-walking helpers.
- `internal/explain/decision_test.go` — render + chain + dedupe tests.
- `internal/testrunner/decisions.go` — `Decisions(prog, plans)`
  produces per-test `[]Decision` with cross-block linking.
- `internal/testrunner/decisions_test.go` — cement detect + recommend
  end-to-end.
- `cmd/tln/main.go` — `runExplain()` and `explain` subcommand.
- `examples/cement_explain.tln`, `test/cement_explain.tln.test` —
  Tier-1 demo fixture.

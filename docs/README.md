# Talon documentation

## Language

- **[REPL walkthrough](./repl.md)** — interactive exploration with `:eval`,
  `:trace`, and an end-to-end insurance-claims example.
- **[FactStore](./factstore.md)** — the database abstraction, structured
  `Query`, MemoryStore, and the `--store=memory` flag.
- **[Observability](./observability.md)** — structured logging,
  `--log-format` / `--log-level` flags, and `on { logger.info "…" }`
  execution.
- **[Optimizers](./optimizers/README.md)** — when to use Pareto vs GA vs
  ILP vs ACO; worked examples; combine block reference.
- **[Defeasible reasoning](./defeasible.md)** — `strict` rules,
  `overrides`, priority-based conflict resolution.
- **[Reactive rules](./reactive.md)** — `on change` / `on assert` /
  `on retract` blocks that fire when facts mutate.
- **[Integrity constraints](./constraints.md)** — `constraint` blocks
  with `require` / `on_violation reject|warn|quarantine`.

## Design notes

- [ADR-0001: ML runtime strategy](./design/0001-ml-runtime-strategy.md)
- [ADR-0003: Explainability tiers](./design/0003-explainability.md)
- [JS runtime](./js-runtime.md)

## Where the code lives

- `cmd/talon/` — CLI entry point (`build`, `test`, `run`, `explain`).
- `internal/lexer`, `internal/parser`, `internal/ast` — front end.
- `internal/validator` — pre-planning checks.
- `internal/planner` — emits `QueryPlan`s of `DatalevinQuery`,
  `MLComputation`, `GoComputation`, `Filter` steps.
- `internal/executor` — runs plans against a real `FactStore`.
- `internal/testrunner` — same dispatch against an in-memory entity
  store for `talon test` / `talon explain`.
- `internal/mlruntime` — 7 ML primitives (anomaly, learned_threshold,
  predict, forecast, cluster, classify, similar).
- `internal/optimize` — Pareto ranking, GA, ACO, ILP.
- `internal/explain` — Tier-1 Decision rendering.
- `pkg/talon/` — public Go SDK for embedding workflows.

## Reading order for new contributors

1. `examples/fleet_maintenance.talon` — every block type except combine.
2. `examples/cement_explain.talon` + the explainability ADR.
3. `examples/fleet_dispatch.talon` — Pareto ranking, simplest combine.
4. `docs/optimizers/README.md` — the optimizer family map.
5. `examples/parts_reorder.talon` (GA), `examples/parts_reorder_exact.talon`
   (ILP), `examples/service_route.talon` (ACO),
   `examples/tuned_consumption.talon` (ABC tuning, anomaly z-threshold),
   `examples/tuned_high_mileage.talon` (ABC tuning, learned percentile)
   — one example per backend.
6. `docs/optimizers/architecture.md` — code layout if you want to add
   another optimizer.

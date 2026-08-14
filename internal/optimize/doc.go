// Package optimize implements multi-objective optimization primitives for
// tln's `combine` block.
//
// v1 ships NSGA-II's fast non-dominated sort + crowding distance from Deb et
// al. (2002). Each row that satisfies a combine selector becomes one
// Individual evaluated against every minimize/maximize objective; the result
// is a ranked population where rank 0 is the Pareto frontier.
//
// The dominated-count, crowding-distance, and rank values flow into the
// Decision chain (see internal/explain) so `tln explain` can answer "why
// did combine pick this entity" with population-relative evidence, the same
// way the ML primitives in internal/mlruntime emit per-row Explanations.
// See ADR-0001 for the explainability contract.
//
// Genetic-algorithm operators (crossover, mutation, tournament selection)
// are intentionally absent in v1 — they earn their keep once the language
// grows a subset/constraint clause (e.g. `select K from`, `subject_to`),
// at which point an Individual becomes a bitmask rather than a single row.
// The Pareto ranking + crowding kernel exported here is reused unchanged
// in that future v2 selection step.
package optimize

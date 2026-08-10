# Talon optimizers

The `combine` block solves a family of optimization problems over selected
records. Talon picks the backend based on the syntax you write — there is
no global setting. This page maps clauses to backends and explains when
each one is the right choice.

## Quick reference

### `combine` block backends — choosing *which records*

| Clauses you wrote | Backend | What you get |
|---|---|---|
| `minimize` / `maximize` only | **NSGA-II Pareto** | Pareto-frontier ranking of individual records |
| `select K from records` + `minimize` / `maximize` (+ optional `subject_to`) | **Genetic algorithm (GA)** | Pareto frontier of subsets, robust under stochastic search |
| `select K from records` + single `minimize` / `maximize` + `solver linear` | **ILP branch-and-bound** | One provably optimal subset |
| `sequence` + `coordinates` | **Ant Colony Optimization (ACO)** | Shortest Hamiltonian tour |

### `detect`/`forecast` block tuning — choosing *what threshold*

| Clauses you wrote | Backend | What you get |
|---|---|---|
| `tune against test "..."` on anomaly detect | **Artificial Bee Colony (ABC)** | Per-tenant z-threshold (`anomaly_zscore`) |
| `tune against test "..."` on learned_threshold detect | **Artificial Bee Colony (ABC)** | Per-tenant percentile (`learned_threshold`) |

ABC tuning is **orthogonal** to the four combine backends: it operates on
the ML primitive inside a detect block, not on a combine block. A single
rule file can use both — e.g. an ABC-tuned anomaly detect feeding a
GA-driven combine reorder. v1 ships tuning for the two implemented
primitives (anomaly + learned_threshold); the other five mlruntime
primitives are language surface only and become tunable when their
implementations ship.

## Decision tree

```
Do you need an *order*, not a *subset*?
├── YES → sequence + coordinates → ACO
└── NO ↓

Do you need to pick K out of N records?
├── NO → minimize/maximize only → NSGA-II Pareto ranking
└── YES ↓

Are all objectives and constraints linear sums of attrs?
└── If yes AND single-objective → solver linear → ILP (exact optimum)
    Otherwise → GA (Pareto frontier of subsets)
```

## Backends in detail

### NSGA-II Pareto ranking (v1 default)

**When**: You have a list of candidate records and want to surface the
ones that are non-dominated across multiple criteria.

**Syntax**:
```talon
combine "Service priority" {
  for records where is "overdue"
  maximize attr "km_overdue"
  maximize attr "daily_revenue"
  minimize attr "estimated_repair_cost"
  return id
}
```

**Output**: Every record gets a `pareto_rank` (0 = on the frontier).
`flagged` contains rank-0 entities. `talon explain` cites which
dimensions each pick wins on and how many other candidates it dominates.

**Algorithm**: Deb et al. (2002) fast non-dominated sort plus crowding
distance. Deterministic; no seed needed.

**Cost**: O(MN²) where M = objectives, N = candidates. Practical up to
~10,000 candidates with 2–3 objectives.

[Worked example →](./pareto-ranking.md)

### Genetic Algorithm with constraints (v2)

**When**: You need to pick exactly **K out of N** records, with optional
inequality constraints on aggregates. Multi-objective trade-offs welcome.

**Syntax**:
```talon
combine "Reorder picks" {
  for records where type == "stock_item" and status == "active"
  select 3 from records
  minimize total(attr "reorder_cost")
  maximize total(attr "downstream_blast_radius")
  subject_to total(attr "reorder_cost") <= 5000
  return id
  seed 42
}
```

**Output**: A Pareto frontier of **subsets** (not individual records).
`flagged` is the union of entities across all rank-0 subsets. Per-entity
`talon explain` shows "selected in N of M Pareto-optimal subsets" — a
robustness signal you can read at a glance.

**Algorithm**: NSGA-II survivor selection with Deb's constraint-dominance:
feasible solutions always dominate infeasible ones; among infeasible,
smaller total violation wins.

**Tunable**: `seed N` for reproducibility. Internal defaults: population
100, 100 generations, 90% crossover, swap-mutation rate 5%.

**Cost**: O(Generations × PopulationSize × N) per generation, plus the
NSGA-II survivor sort. Scales to thousands of candidates.

[Worked example →](./subset-ga.md)

### Integer Linear Programming (v2.1)

**When**: Same shape as the GA, but **single objective** and **linear
sums only** (`total(attr)` and `count(records)` — no `avg`, no products).
ILP returns a provably optimal answer in milliseconds for typical
business-size problems.

**Syntax**:
```talon
combine "Reorder exact" {
  for records where type == "stock_item" and status == "active"
  solver linear
  select 3 from records
  maximize total(attr "downstream_blast_radius")
  subject_to total(attr "reorder_cost") <= 5000
  return id
}
```

**Output**: One subset; `flagged` is exactly that subset. `talon explain`
declares "part of the provably optimal subset" — no probabilistic hedging.

**Algorithm**: 0/1 branch-and-bound with LP-relaxation bounding. Pure Go,
no native solver dependencies.

**When NOT to use**:
- Multi-objective → drop `solver linear`, use the GA
- Nonlinear objectives or constraints (e.g., `avg()`) → GA
- Very large N (≫1000 candidates) where exact is too slow → GA gives
  good approximations

[Worked example →](./ilp-exact.md)

### Ant Colony Optimization (v2.1)

**When**: The decision is an **ordering** — visit sequence, traversal
order, route. Every selected candidate must be visited exactly once.

**Syntax**:
```talon
combine "Service tour" {
  for records where is "overdue"
  sequence
  coordinates attr "yard_x", attr "yard_y"
  return id
  seed 42
}
```

**Output**: `flagged` is the entities in optimal visit order. Each entity
gets a `stop_number` in evidence. `talon explain` renders "stop 3 of 7
on the shortest tour (length 28.4)".

**Algorithm**: Classic Ant System with elitist reinforcement (Dorigo 1992).
Pheromone trails on graph edges; each iteration, ants probabilistically
build tours weighted by (τ^α · η^β); best-so-far tour gets extra
pheromone deposit.

**Tunable**: `seed N`. Internal defaults: 20 ants, 100 iterations, α=1,
β=3, evaporation ρ=0.1.

**Distance model**: Euclidean from two coordinate attrs. For
non-euclidean distances (driving time, network hops, custom cost), open
an issue — the runtime needs a `distance_matrix` clause that doesn't
exist yet.

[Worked example →](./aco-routing.md)

### Artificial Bee Colony — ML primitive auto-tuning

**When**: A detect/forecast block uses an ML primitive whose threshold
should adapt to per-tenant data. The default z = 2.5 is fine for normally-
distributed values but miscalibrates against heavy-tailed, cyclical, or
batched data. v1 ships tuning for `anomaly_zscore`; other primitives will
follow.

**Syntax**:
```talon
detect "Tuned consumption anomaly" {
  for records where type == "stock_item"
    and attr "weekly_consumption" is anomaly compared_to last 12 weeks
  tune against test "labeled_consumption_history"
  flag matching items
}
```

**Output**: A tuned threshold injected into the primitive's `Params` at
evaluation time. Each Decision cites the fixture, F1, precision, and
recall that justified the chosen value.

**Algorithm**: Karaboga's Artificial Bee Colony (2005). 16 bees × 40
iterations searching threshold ∈ [0.5, 4.0]. Fitness = F1 against the
labeled fixture. The scout phase automatically escapes local optima —
no risk of locking onto a bad threshold because of unlucky initialization.

[Worked example →](./abc-tuning.md)

## Choosing between GA and ILP

You can write almost the same `combine` block with either backend; the
trade-offs:

| | GA | ILP |
|---|---|---|
| Objectives | Many | Single |
| Constraints | Multiple | Multiple, linear only |
| Aggregates | total, count, avg | total, count (no avg) |
| Result | Pareto frontier of subsets | One optimal subset |
| Determinism | With seed, yes | Always |
| Audit narrative | "selected in N of M Pareto subsets" | "provably optimal" |
| Scales to N ≈ | 10,000 | ~1,000 (problem-shape-dependent) |
| Recommended when | You want trade-off visibility | Auditors want certainty |

A typical real-world rollout: ILP for the production decision, GA at
review time to show stakeholders the alternatives that were *close to
optimal* on different trade-offs.

## Future backends

The roadmap, with v2.1 + v3 already shipped:

- ✅ **NSGA-II Pareto** (v1)
- ✅ **GA with constraints** (v2)
- ✅ **ILP exact** (v2.1)
- ✅ **ACO routing** (v2.1)
- ✅ **ABC for anomaly tuning** (v3)
- ✅ **ABC for learned_threshold percentile** (v3.1)
- 🚧 **ABC for remaining primitives** — `forecast` α, `classify_knn` k,
  DBSCAN ε. Each blocks on the primitive itself shipping; the tuning
  registry takes ~5 LOC per primitive once that's done.
- ⏳ **SPEA2 / NSGA-III** — many-objective Pareto (4+ dimensions); NSGA-II
  degrades there.
- ⏳ **Simulated Annealing** — single-solution alternative to GA when
  population overhead isn't worth it.
- ⏳ **Constraint Programming (CP-SAT)** — scheduling problems with
  temporal constraints.

If you have a real problem one of these would solve, open an issue with
the `.tln` snippet you'd want to write — the language surface drives
the backend selection, not the other way around.

# Ant Colony Optimization — worked example

When the decision is an **ordering**, not a subset, tln routes the
`combine` block to Ant Colony Optimization. Use `sequence` instead of
`select K from records`.

## Problem

A service technician will visit every overdue vehicle in the depot
yard today. The visit order matters — total walking distance scales
non-linearly with sequence. We want the shortest Hamiltonian tour.

## tln source

```tln
define "needs_service" {
  type == "item" and category == "Vehicles" and status == "overdue"
}

combine "Service tour" {
  for records where is "needs_service"
  sequence
  coordinates attr "yard_x", attr "yard_y"
  return id, yard_x, yard_y
  label "Stop {item.name} at ({attr.yard_x}, {attr.yard_y})"
  seed 42
  priority HIGH
}
```

Compiles to:

```
DatalevinQuery → candidates    # selector + 2 coordinate attrs bound
GoComputation  optimize_aco(candidates) → tour
```

## Key clauses

- `sequence` — opt in to ACO. Without this, `combine` routes to ranking
  or subset selection.
- `coordinates attr "X", attr "Y"` — two attrs that name a 2-D position.
  ACO computes pairwise euclidean distance from these.
- No `minimize` or `maximize` — the objective is implicit (minimize
  total tour length). Validator rejects them.
- No `select K` — sequence mode visits every candidate, never a subset.
- No `subject_to` — not yet supported in sequence mode.

## What `tln explain` shows

```
COMBINE   Service tour — stop 1 of 4 on the shortest tour (length 40.00 via (yard_x, yard_y))
ITEM      Van B  (entity #202)
EVIDENCE
  stop_number  = 1
  total_stops  = 4
  tour_length  = 40
```

Each entity gets its `stop_number` (1-indexed; 1 is the start). The
tour is a cycle — the last stop is implicitly followed by stop 1.

## Algorithm

Classic Ant System with elitist reinforcement (Dorigo, 1992):

1. **Initialization** — every edge `(i, j)` starts with a small uniform
   pheromone level.
2. **Per iteration**:
   - 20 ants each build a Hamiltonian cycle. At each step, the next
     node is chosen probabilistically: weight = `pheromone^α · (1/distance)^β`.
   - All pheromone evaporates by ρ (default 0.1).
   - Each ant deposits `Q / tour_length` on every edge of its tour.
   - The best-so-far tour is reinforced once more (elitism).
3. After 100 iterations, return the best tour found.

Parameters (α=1, β=3, ρ=0.1, Q=100, 20 ants × 100 iterations) come
straight from the original paper. Set `seed N` for reproducibility.

## Coordinate semantics

The two attrs you pass to `coordinates` define a euclidean metric:

```
distance(i, j) = sqrt( (X_i - X_j)² + (Y_i - Y_j)² )
```

For non-euclidean costs (driving time, network hops, fixed cost matrix),
this isn't yet expressible. Pre-compute a 1-D "embedding" via MDS and
store as a `yard_x` / `yard_y` pair as a workaround, or open an issue
to design a `distance_matrix` clause.

## Convergence trace

The ACO engine emits a per-iteration `history` of best-so-far tour
length. This is non-increasing — the printed final length is the
plateau. If you suspect the algorithm hasn't converged (large jumps
late in the trace), increase iterations or check that the coordinate
spread isn't pathological (all points near-collinear, e.g., where the
heuristic 1/distance becomes very noisy).

## When NOT to use sequence mode

- **You're picking a subset, not an order** — use `select K from records`.
- **All candidates are equally close** — sequence is degenerate; just
  return them as a flagged set.
- **Costs are not euclidean and not encodable as 2-D positions** —
  not yet supported. ILP / OR-tools would be the right tool here.

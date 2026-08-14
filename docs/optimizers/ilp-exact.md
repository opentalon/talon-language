# ILP exact solver — worked example

When the problem is linear and single-objective, tln can solve it to
provable optimality instead of approximating with the GA. The syntax is
the same; you opt in with `solver linear`.

## Problem

Procurement wants the **provably optimal** reorder pick — not a Pareto
frontier, not a probability distribution, one answer to defend in an
audit:

> "We picked these 3 parts because no other subset of 3 active stock items
> within the $5000 budget covers more downstream work orders."

## tln source

```tln
combine "Reorder exact" {
  for records where type == "stock_item" and status == "active"
  solver linear
  select 3 from records
  maximize total(attr "downstream_blast_radius")
  subject_to total(attr "reorder_cost") <= 5000
  return id, reorder_cost, downstream_blast_radius
  label "Reorder {item.name}: ${attr.reorder_cost}, blocks {attr.downstream_blast_radius} jobs"
  priority HIGH
}
```

Compiles to:

```
DatalevinQuery → candidates    # selector + 2 attrs bound (cost, blast_radius)
GoComputation  optimize_ilp(candidates) → frontier
```

Note: `frontier` is a vestigial variable name from the GA path. With
ILP, the variable holds **one** subset, not a frontier.

## What `tln explain` shows

```
COMBINE   Reorder exact — part of the provably optimal subset (total(downstream_blast_radius) = 30)
ITEM      Oil Filters  (entity #502)
WHY
  • part of the provably optimal subset (total(downstream_blast_radius) = 30)
EVIDENCE
  exact_optimum                       = true
  subset_size                         = 3
  total(downstream_blast_radius)      = 30
```

No "selected in N of M subsets" because there is only one subset. The
audit story is stronger but the trade-off visibility is gone.

## Algorithm

0/1 branch-and-bound with LP-relaxation bounding:

1. Order candidates by per-unit objective desirability (greedy heuristic).
2. Branch: at each step, fix the next variable to 1 (take it) or 0 (skip it).
3. Bound: at each node, compute the LP relaxation upper bound — if it
   can't beat the current best, prune the subtree.
4. At each leaf, check every constraint; if feasible and better than
   current best, save.

Returns when the search tree is exhausted. Pure Go, no native solver
dependencies, no SAT-style heuristic shortcuts.

## When this works

Every objective and constraint must be a **linear sum of per-row
contributions**. Specifically:

| Expression | Allowed? |
|---|---|
| `total(attr "x")` | ✅ |
| `count(records)` | ✅ (every row contributes 1) |
| `count(attr "x")` | ✅ (rows where attr != 0 contribute 1) |
| `attr "x"` (in `select K` mode) | ✅ (synonymous with `total`) |
| `avg(attr "x")` | ❌ — nonlinear in subset size |
| Multiple `minimize` / `maximize` | ❌ — exact multi-objective is exponential |
| Products of attrs | ❌ — would need quadratic programming |

Validator errors out at compile time for any of these — so you can't
ship a broken ILP plan to production.

## When to NOT use ILP

- **You want to see alternatives** — the GA's Pareto frontier of
  approximations is more informative.
- **N is huge (≫1000 candidates) and dense** — branch-and-bound's
  worst case is exponential. GA degrades gracefully; ILP can hang.
  Open an issue if you hit this; warm-starting from a GA solution
  is a known mitigation.
- **You'd rather change syntax than backend** — `combine` without
  `solver linear` always works; ILP is an opt-in optimization.

## Diagnostic: nodes_explored

The ILP result includes a `nodes_explored` counter — the size of the
branch-and-bound tree the solver walked. A few hundred is fast (sub-ms);
millions means the LP relaxation isn't pruning effectively (usually a
sign of weak bounds, e.g., many similar-coefficient items).

For business-sized problems (≤100 candidates, ≤5 constraints), expect
< 10,000 nodes.

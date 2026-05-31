# Subset GA — worked example

When the question is "pick K out of N records under constraint X", Talon
routes the `combine` block to a genetic algorithm with NSGA-II selection
and Deb's constraint-dominance rule. The result is a Pareto frontier of
*subsets*; the explainability narrative is robustness — how often each
record appears across optimal subsets.

## Problem

Procurement reorders spare parts weekly. Each part has:

- `reorder_cost` — cash to buy one unit
- `days_until_stockout` — small = urgent
- `downstream_blast_radius` — how many open work orders stall on a stockout

The buyer can place 3 orders per week and has a $5000 budget. Pick the
right 3.

## Talon source

```talon
combine "Reorder picks" {
  for records where type == "stock_item" and status == "active"
  select 3 from records
  minimize total(attr "reorder_cost")
  minimize total(attr "days_until_stockout")
  maximize total(attr "downstream_blast_radius")
  subject_to total(attr "reorder_cost") <= 5000
  return id, reorder_cost, days_until_stockout, downstream_blast_radius
  label "Reorder {item.name}: ${attr.reorder_cost}, {attr.days_until_stockout}d to stockout, blocks {attr.downstream_blast_radius} jobs"
  seed 42
  priority HIGH
}
```

Compiles to:

```
DatalevinQuery → candidates    # selector + 3 attrs bound (cost, urgency, blast_radius)
GoComputation  optimize_ga(candidates) → frontier
```

## What `talon explain` shows

```
COMBINE   Reorder picks — selected in 100 of 100 Pareto-optimal subsets
ITEM      Oil Filters  (entity #502)
WHY
  • selected in 100 of 100 Pareto-optimal subsets on (total(reorder_cost), total(days_until_stockout), total(downstream_blast_radius))
EVIDENCE
  subset_members                    = 501,502,503
  subset_rank                       = 0
  subset_size                       = 3
  subset_total(days_until_stockout) = 12
  subset_total(downstream_blast_radius) = 26
  subset_total(reorder_cost)        = 2600
```

## How to read "selected in N of M subsets"

This is the v2 insight v1 couldn't give:

- **100/100** = unanimous pick. Every Pareto-optimal subset includes
  this record. Order it.
- **67/100** = strong but situational. Some trade-offs (e.g., extra
  budget room) favor a different choice. Worth a second look.
- **0/100** = never selected. Either dominated or always exceeds a
  constraint.

Stakeholders read "Oil Filters appear in 100% of optimal subsets" with
zero math background. That's the auditability win.

## Seed and determinism

GA is stochastic — without a seed you get slightly different frontiers
across runs. `seed 42` (or any constant) makes runs reproducible for
tests and audits. Production code probably wants a fresh seed each run
to avoid local-optimum lock-in across weeks; tests want a fixed seed
for stable assertions.

## When to switch backends

- **Single objective, all linear** → `solver linear`. Provably optimal,
  no robustness narrative needed.
- **Visit order matters, not subset** → `sequence` + `coordinates`. ACO.
- **Decision is "is this record one of the top picks"** without a
  hard limit → drop `select K`, use Pareto ranking.

## Tuning

Internal defaults work for most business-sized problems. If you need to
tune (rare):

- **Slow convergence** → check that constraints aren't masking the
  feasible region. The GA wastes generations exploring infeasible
  space before constraint-dominance pulls it back.
- **Same answer every run with seed** → expected. Vary the seed if you
  want diversity.
- **No feasible subset returned** → the `subject_to` is impossible at
  the requested `select K` size. `talon explain` will show an empty
  frontier; relax the constraint or shrink K.

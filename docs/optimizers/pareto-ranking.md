# Pareto ranking — worked example

The simplest `combine` shape: multi-objective ranking over individual records.
No subset selection, no constraints — every selected record is one decision
point, and tln labels which ones lie on the Pareto frontier.

## Problem

The garage has 50 vehicles overdue for service. Mechanics can touch 5
today. Which 5? Three competing criteria:

- **How overdue** — `km_overdue` (we'd like a big number)
- **Daily revenue lost** — `daily_revenue` (we'd like a big number — high earners get priority)
- **Estimated repair cost** — `estimated_repair_cost` (we'd like a small number)

There are no weights any of us trust. Pareto ranking surfaces the
genuine trade-offs and a human picks 5 from the frontier.

## tln source

```tln
define "overdue" {
  type == "item" and category == "Vehicles"
  and attr "km" > attr "last_service_km"
}

combine "Service priority" {
  for records where is "overdue"
  maximize attr "km_overdue"
  maximize attr "daily_revenue"
  minimize attr "estimated_repair_cost"
  return id, km_overdue, daily_revenue, estimated_repair_cost
  label "{item.name}: {attr.km_overdue} km overdue, ${attr.daily_revenue}/day, repair ${attr.estimated_repair_cost}"
  priority HIGH
}
```

The plan compiles to:

```
DatalevinQuery → candidates    # selector + 3 objective attrs bound
GoComputation  optimize_pareto(candidates) → frontier
```

## What `tln explain` shows

For each entity on rank 0 (the Pareto frontier), the output cites the
objective values, the pareto_rank, and the **crowding distance** — `+Inf`
on boundary points (extreme on at least one objective), a finite number
for interior frontier points. Boundary points are useful because they
guarantee one objective is maximized in isolation.

```
COMBINE   Service priority — non-dominated on (km_overdue, daily_revenue, estimated_repair_cost); rank 0; dominated 0 of 47 candidates
ITEM      Truck A  (entity #101)
WHY
  • non-dominated on (km_overdue, daily_revenue, estimated_repair_cost); rank 0; dominated 0 of 47 candidates
EVIDENCE
  km_overdue            = 25000
  daily_revenue         = 600
  estimated_repair_cost = 1200
  pareto_rank           = 0
  crowding_distance     = 0.47
```

## How a human reads the frontier

Most useful question: which dimension does each frontier point WIN on?

- **+Inf crowding on `daily_revenue`** → this vehicle has the highest
  revenue of any frontier point. Don't ignore it.
- **+Inf crowding on `estimated_repair_cost`** → cheapest to fix of any
  frontier point. Pick if budget is tight.
- **Finite crowding** → interior of the frontier; balanced trade-off.

Combined with the `dominated X of N candidates` count, the manager has a
short list of meaningfully different choices instead of one synthetic
"score."

## When NOT to use this shape

- **You want to limit to top K** — add `select K from records`; routes
  to the GA.
- **You have a budget or capacity constraint** — same, plus `subject_to`.
- **All you need is "highest score wins"** — `combine` is overkill;
  add a single computed attr and sort it in a normal `detect`.

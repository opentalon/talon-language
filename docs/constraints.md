# Integrity constraints

A `constraint` block is an invariant that must hold for every record
matching its selector. Constraints run on every fact mutation — assert,
retract, batch load — and decide whether a candidate fact is allowed
into the FactStore.

Think of them as database CHECK constraints, but expressed in tln and
spanning the full fact graph (not just one table).

## Why

Facts arrive from MCP tools — external systems with bugs. They might
emit a `status: "actvie"` typo, a negative stock count, or an activity
referencing an item that doesn't exist. If those facts land unfiltered,
every rule downstream produces wrong results.

Constraints catch bad facts at the boundary, before they poison the
graph.

## Syntax

```tln
constraint "Item status is valid" {
  for records where type == "item"
  require attr "status" in ["active", "defective", "missing", "inactive"]
  on_violation reject "invalid item status — typo upstream?"
}

constraint "Stock cannot be negative" {
  for records where type == "stock_item"
  require attr "current_stock" >= 0
  on_violation reject "stock must be non-negative"
}

constraint "Activity date not in the future" {
  for records where type == "activity"
  require attr "activity_date" <= 0
  on_violation quarantine "needs admin review"
}
```

The `require` clause is the predicate that must hold. If it evaluates
false for a matching record, the constraint is violated.

## Violation modes

| Mode         | Behavior                                                        |
| ------------ | --------------------------------------------------------------- |
| `reject`     | Fact is not asserted. Error returned to the caller.             |
| `warn`       | Fact is asserted; a warning is logged. Evaluation continues.    |
| `quarantine` | Fact lands in a separate quarantine store, hidden from rules.   |

When multiple constraints apply and more than one fails, the most
severe outcome wins (`reject > quarantine > warn`).

## When constraints run

- **On assert** — before a new fact enters the store.
- **On retract** — before a fact is removed (referential checks: "don't
  delete a person who still has active assignments").
- **On batch load** — constraints run per-batch; violations roll up into
  a single report rather than failing on the first one.

## Runtime

`internal/constraints.Check(record, blocks)` evaluates a candidate
record against a slice of constraint blocks and returns a `Verdict`
that names the chosen mode and the per-constraint reasons. The package
implements a minimal per-record evaluator: comparisons, membership,
boolean combinations, and string match. Cross-record / referential
constraints (`references record where ...`) will land alongside a
FactStore implementation that exposes the rest of the graph to the
evaluator.

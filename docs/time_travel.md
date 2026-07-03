# Time-travel queries — `was ( … ) N <unit> ago`

Most detect conditions ask about the present. A time-travel condition asks
about the **past**: did the inner condition hold about this record *N units
ago*? It is how an agent reasons about change — "flag machines that are
defective now but were certified 90 days ago."

```talon
detect "Certification regressed" {
  for records where type == "machine"
    and status == "defective"
    and was (status == "certified") 90 days ago
  flag matching items
}
```

## Semantics

`was ( <inner> ) N <unit> ago` contributes a two-part narrowing:

```
result = candidates(now)  ∩  { records where <inner> held as of now − N·unit }
```

- **candidates(now)** — the records matching the rest of the selector today
  (here: `type == "machine" and status == "defective"`).
- **as-of set** — the records for which `<inner>` was true at the instant
  `now − N·unit`, evaluated against the store's *historical* state.

A record is flagged only if it is in both sets. Multiple `was … ago`
conjuncts chain (each intersects the running candidate set).

## How it runs

The planner lowers the block into three steps: a present-day `FactQuery`
for the candidates, a second `FactQuery` for the inner condition tagged
with the delta (`AsOfDelta`), and an `asof_intersect` that joins them on
entity id. The executor runs the tagged query through the FactStore's
`TimeTraveler` capability:

```go
type TimeTraveler interface {
    QueryAsOf(ctx context.Context, q Query, asOf time.Time) ([][]any, error)
}
```

`asOf` is `executor now − delta`. A store that doesn't implement the
capability makes the block fail with `factstore.ErrNoTimeTravel`.

## Backend support

| Backend      | Time-travel | Mechanism |
|--------------|-------------|-----------|
| MemoryStore  | ✅          | append-only per-cell version history; reconstructs a snapshot at `asOf` |
| talon-db     | ✅          | per-document version history in bbolt + a `QueryAsOf` gRPC RPC |
| Datalevin    | ❌          | Datalevin 0.10.7 has no point-in-time primitive (no `d/as-of`); returns `ErrNoTimeTravel`. App-level history is a tracked follow-up. |

Only the FactStore's *write history* is consulted — for each cell, the
value in effect at `asOf` (or none, if the cell was created later or
retracted by then). Facts written before history tracking existed are
invisible to as-of queries.

## Restrictions (v1)

Enforced by the validator / planner:

- Must be a **top-level `and` conjunct** of a `detect` or `rule` selector.
  Nested inside `or` / `not` it is rejected (the planner can't lower it
  there and would otherwise drop it silently).
- The **inner condition must be Datalog-expressible** — plain
  attribute / type / status comparisons. No nested temporal, date
  arithmetic, or `was … ago` inside the inner condition.

## Try it

A runnable, deterministic demo against a real (in-process) talon-db lives
in [`examples/time_travel`](../examples/time_travel/):

```
go run ./examples/time_travel
```

It seeds four machines "90 days ago" (all certified but one), regresses two
to defective "today", then runs the detect above — flagging exactly the two
that regressed.

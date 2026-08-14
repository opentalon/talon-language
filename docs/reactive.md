# Reactive rules

Reactive rules fire **when a fact changes**, not on request or on schedule.
They close the latency gap that polling leaves open: if stock drops below
minimum at 10:01 and the next cron tick is at 10:05, that's four minutes
of nobody knowing. For safety-critical detections, that's too long.

## Syntax

```tln
// Fire when an attribute changes.
on change attr "current_stock" {
  when new_value <= record.attr "minimum_amount"
  logger.warn "stock threshold crossed: {item.name}"
  recommend "Order stock"
}

// Fire when a new fact of the given type is asserted.
on assert activity {
  detect "Defective item without ticket"
}

// Fire when a fact is removed.
on retract item {
  logger.warn "item removed: {item.id}"
}
```

`on change` optionally narrows to a specific target value:

```tln
on change attr "status" to "defective" { ... }
```

## Triggers

| Trigger              | Fires when                                          |
| -------------------- | --------------------------------------------------- |
| `on change attr "x"` | An existing fact's value for attribute `x` changed  |
| `on change attr "x" to <expr>` | Changed *to* a specific value             |
| `on assert <type>`   | A new fact of the given type was added              |
| `on retract <type>`  | A fact of the given type was removed                |

## Body actions

For the minimal implementation, on-block bodies support:

- `logger.info "..."`, `logger.warn "..."`, `logger.error "..."` —
  structured log lines with template interpolation.
- `recommend "Name"` — invoke a named recommend block by reference.
- `detect "Name"` — invoke a named detect block by reference.

An optional `when` clause filters which events actually fire the body.

## Runtime

`internal/factstore.EventEmitter` is a small fan-out helper any FactStore
implementation can embed to gain `Subscribe` / `Emit`.
`internal/reactive.Dispatcher` registers OnBlocks and routes matching
events to a caller-supplied `ActionHandler`, which evaluates the
block's `when` clause and runs the body.

Reactive evaluation is per-change, not per-tick. Cost scales with the
frequency of fact mutations, not with the size of the fact graph.

## Relationship to scheduled detection

| | `detect` block            | `on` reactive rule          |
| --- | ----------------------- | --------------------------- |
| When | On request or schedule | When a fact changes        |
| Scans | All matching facts    | Only the changed fact       |
| Best for | "Show me everything overdue" | "Alert when something becomes overdue" |
| Cost | Proportional to data size | Proportional to change frequency |

Use both: `detect` for batch sweeps and dashboards, `on` for real-time
response.

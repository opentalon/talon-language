# Refill watcher agent — console demo

A runnable, end-to-end demo of a Talon watcher agent built on the
`pkg/talon` reactive [`Session`](../../pkg/talon/session.go) API. It shows
the full loop a real agent runs: **fetch → map to facts → detect a
change → fire a workflow → act**.

```
go run ./examples/refill_agent
```

`refill_agent.talon` holds the program: an `on change attr "current_stock"
to 0` block that logs a warning and runs the `Refill stock` workflow,
which places an order via the `inventory` MCP tool.

`main.go` drives it: it simulates two inventory fetches, maps each item's
stock into facts, and asserts them into the Session. The `inventory` MCP
call is wired to a console stand-in that prints the refill action.

Expected output:

```
=== refill watcher agent ===
program loaded; watching attr "current_stock" for transitions to 0

[tick 1] fetch dump:
  cement   current_stock=8
  sand     current_stock=3
  → no firings

[tick 2] fetch dump:
  cement   current_stock=0
  sand     current_stock=3
... level=WARN msg="stock-out detected for item 1" ...
    ↻ refilled cement — [inventory.create-refill-order] qty=50 → order PO-1001
  → [on change attr "current_stock"] fired workflow "Refill stock"

done.
```

Tick 1 seeds the stock levels (an *assert*, no change → no firing). Tick 2
drains cement to 0, which is a *change to 0* — the guard matches, the
workflow fires exactly once, and the trigger's entity is threaded into the
order via `step("trigger").result.entity`.

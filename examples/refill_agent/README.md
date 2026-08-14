# Refill watcher agent — REPL demo

An end-to-end demo of a tln watcher agent, driven interactively from
`tln repl`. It shows the loop a real agent runs: **assert facts →
detect a change → fire a workflow → act**.

`refill_agent.tln` holds the program: an `on change attr "current_stock"
to 0` block that logs a warning and runs the `Refill stock` workflow,
which places an order via the `inventory` MCP tool.

## Run it

Preload the program into the REPL, then assert facts:

```
$ tln repl examples/refill_agent/refill_agent.tln
  loaded examples/refill_agent/refill_agent.tln: 2 block(s), 0 fact(s)
  watching: 1 on-block(s) armed — assert facts to fire them
tln> attr 1 "current_stock" 8
  OK: attr 1
tln> attr 1 "current_stock" 0
  ... level=WARN msg="stock-out detected for item 1" ...
  ↻ mcp inventory.create-refill-order {item_id=1 quantity=50}
  ✓ [on change attr "current_stock"] fired workflow "Refill stock"
tln> :quit
```

Loading a program that contains `on` blocks arms the REPL as a live
watcher (backed by the `pkg/tln` reactive Session). Each fact you
assert is pushed into the session; when `current_stock` transitions to 0
the guard matches, the workflow fires exactly once, and its `mcp` step is
routed to a console stand-in that prints the refill action. The trigger's
entity is threaded into the order via `step("trigger").result.entity`.

Setting stock to 8 first, then 0, is what makes it a *change to 0* — the
first assert just establishes the value (no firing), the second crosses
the threshold. Re-asserting 0 does nothing: an unchanged value emits no
event.

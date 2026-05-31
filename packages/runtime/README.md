# @opentalon/talon-runtime

Reactive rule engine for [Talon](https://github.com/opentalon/talon-language) — a path-based reactive store that evaluates Talon rules on every state change and dispatches resulting actions to subscribers and registered handlers.

This is the **JavaScript runtime**, intended for browser and Node applications that want to embed Talon's reactive rule subset (the lexer, parser, evaluator, and `TalonStore`). For workflow execution, ML primitives, and the full CLI, see the Go implementation in the main repo.

## Install

```bash
npm install @opentalon/talon-runtime
```

## Quick start

```ts
import { TalonStore } from "@opentalon/talon-runtime"

const store = new TalonStore()

store.loadRules(`
  rule "Show delivery address" {
    when "product_type" != "digital"
    do show "delivery_address"
  }

  rule "Recalculate subtotal" {
    when "unit_price" changes or "quantity" changes
    do set "subtotal" to "unit_price" * "quantity"
  }
`)

store.subscribe("*", (actions) => {
  for (const a of actions) {
    console.log(a.rule, a.verb, a.args)
  }
})

store.set("product_type", "physical")
store.set("unit_price", 100)
store.set("quantity", 5)

console.log(store.get("subtotal")) // 500
```

## Features

- **Path-based state** — flat keys (`"unit_price"`) or nested paths (`"booking.items.0.qty"`).
- **Reactive evaluation** — every `set()` re-evaluates affected rules.
- **Tagged rule sets** — `loadRules(src, { tag })` / `unloadRules(tag)` for dynamic rule swapping.
- **Built-in actions** — `set` and `validate` are handled internally; everything else is dispatched.
- **Custom action handlers** — `store.registerAction("show", (path) => …)`.
- **Prefix subscriptions** — `store.subscribe("booking.*", …)` only fires for matching actions.
- **`define` blocks** — name a condition once, reuse it via `when is "name"`.

## API

```ts
const store = new TalonStore()

store.loadRules(source: string, opts?: { tag?: string }): void
store.unloadRules(tag: string): void

store.set(path: string, value: unknown): void
store.get(path: string): unknown

store.registerAction(verb: string, handler: (...args: any[]) => void): void
store.subscribe(prefix: string, cb: (actions: ActionResult[]) => void): Unsubscribe
```

## Scope

The JS runtime implements the reactive subset of Talon — rules, `define`, conditions, and actions. Workflows, ML primitives, `forecast`/`detect` blocks, and `.talon.test` files are Go-only. See the [main repo](https://github.com/opentalon/talon-language) for the full language.

## License

Apache-2.0

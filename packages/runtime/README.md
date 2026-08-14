# @opentalon/tln-runtime

Reactive rule engine for [tln](https://github.com/opentalon/tln-language) — a path-based reactive store that evaluates tln rules on every state change and dispatches resulting actions to subscribers and registered handlers.

This is the **JavaScript runtime**, intended for browser and Node applications that want to embed tln's reactive rule subset (the lexer, parser, evaluator, and `TlnStore`). For workflow execution, ML primitives, and the full CLI, see the Go implementation in the main repo.

## Install

```bash
npm install @opentalon/tln-runtime
```

## Quick start

```ts
import { TlnStore } from "@opentalon/tln-runtime"

const store = new TlnStore()

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
const store = new TlnStore()

store.loadRules(source: string, opts?: { tag?: string }): void
store.unloadRules(tag: string): void

store.set(path: string, value: unknown): void
store.get(path: string): unknown

store.registerAction(verb: string, handler: (...args: any[]) => void): void
store.subscribe(prefix: string, cb: (actions: ActionResult[]) => void): Unsubscribe
```

## Scope

The JS runtime implements the reactive subset of tln — rules, `define`, conditions, and actions. Workflows, ML primitives, `forecast`/`detect` blocks, and `.tln.test` files are Go-only. See the [main repo](https://github.com/opentalon/tln-language) for the full language.

## License

Apache-2.0

# JavaScript Runtime

The `@tln-lang/runtime` package lets you load tln rules and evaluate them
reactively in JavaScript or TypeScript. When state changes the store
automatically re-evaluates every rule and emits the resulting actions.

## Install

```bash
npm install @tln-lang/runtime
```

## Quick start

```ts
import { TlnStore } from "@tln-lang/runtime";

const store = new TlnStore();

store.loadRules(`
  rule "Show delivery" {
    when "product_type" != "digital"
    do show "delivery_address"
  }
`);

store.subscribe("*", (actions) => {
  for (const a of actions) {
    console.log(a.verb, a.args); // "show" ["delivery_address"]
  }
});

store.set("product_type", "physical"); // rule fires
```

## Computed values with `set … to`

Rules can write back to the store. Downstream rules re-evaluate automatically.

```ts
store.loadRules(`
  rule "Calc subtotal" {
    when "unit_price" changes or "quantity" changes
    do set "subtotal" to "unit_price" * "quantity"
  }
`);

store.set("unit_price", 100);
store.set("quantity", 5);

console.log(store.get("subtotal")); // 500
```

## Validation

The built-in `validate` action checks values against patterns or numeric
ranges.

```ts
store.loadRules(`
  rule "Validate email" {
    when "email" changes
    do validate "email" pattern ".+@.+"
  }

  rule "Check quantity" {
    when "quantity" changes
    do validate "quantity" min 1 max 100
  }
`);

store.subscribe("*", (actions) => {
  for (const a of actions) {
    if (a.verb === "validate") {
      const [path, result] = a.args;
      console.log(path, result.valid, result.message);
    }
  }
});

store.set("email", "bad");      // "email" false "…"
store.set("email", "a@b.com");  // "email" true  ""
store.set("quantity", 0);       // "quantity" false "…"
```

## Reusable conditions with `define`

Named condition blocks can be referenced by any rule with `is`.

```ts
store.loadRules(`
  define "high_value" {
    "booking.total" > 10000
  }

  rule "Needs approval" {
    when is "high_value"
    do require "manager_approval"
  }
`);

store.set("booking.total", 15000); // rule fires
```

## Custom actions

Register handlers for verbs that are not built-in.

```ts
store.registerAction("show", (path) => {
  document.getElementById(path)!.hidden = false;
});

store.registerAction("hide", (path) => {
  document.getElementById(path)!.hidden = true;
});
```

## Tagged rule sets

Load and unload groups of rules independently.

```ts
store.loadRules(`rule "A" { do show "x" }`, { tag: "step1" });
store.loadRules(`rule "B" { do show "y" }`, { tag: "step2" });

store.unloadRules("step2"); // only rule A remains
```

## Nested paths

Dot-notation paths resolve into nested objects and arrays.

```ts
store.set("addons", [{ selected: false, name: "Warranty" }]);

store.loadRules(`
  rule "Addon selected" {
    when "addons.0.selected" == true
    do emit "addon_changed"
  }
`);

store.set("addons", [{ selected: true, name: "Warranty" }]); // fires
```

## Subscribing by prefix

Subscribers can filter actions by path prefix.

```ts
store.subscribe("booking.*", (actions) => {
  // only actions whose first arg starts with "booking."
});
```

## Lower-level API

You can use the lexer, parser, and evaluator directly if you need to inspect
the AST or evaluate conditions outside the store.

```ts
import { lex, parse, evaluate, resolveExpr } from "@tln-lang/runtime";

const tokens = lex(`rule "R" { when "x" > 1 do show "y" }`);
const program = parse(tokens);

console.log(program.rules[0].name); // "R"
```

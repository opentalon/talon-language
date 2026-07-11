# Derived predicates (`derive`)

A `derive` block names a boolean predicate over a record, computed from a
pattern — and any other block can reference it exactly like an asserted fact.
It closes the deductive cycle: multi-step reasoning that used to be assembled
in host code now lives in the language.

```talon
derive overdue(v) {
  for records where type == "vehicle"
    and attr "km" > attr "last_service_km" + 20000
}

detect "Recall candidates" {
  for records where overdue(v)
    and attr "model" in ["Transit", "Sprinter"]
  flag matching items
}
```

`overdue(v)` is defined once; the `detect` reaches for it and adds its own
filter. Block B no longer has to repeat block A's pattern.

## Syntax

```
derive <name>(<var>) {
  for records where <conditions>
}
```

- `<name>` — the predicate name, referenced as `<name>(<var>)` elsewhere.
- `<var>` — the head variable (arity 1). It stands for "the record"; the name
  is cosmetic, matching Datalog surface syntax.
- body — the ordinary selector grammar. Conditions may reference other derived
  predicates, so derivations chain.

A `<name>(<var>)` reference is a condition, usable anywhere a selector
condition is — in `detect`/`rule`/`recommend`/`constraint` selectors, and in
other `derive` bodies.

## How it works — inlining

A derived predicate is **inlined at plan time**: when a block references
`overdue(v)`, the planner substitutes `overdue`'s body conditions into that
block's query. So the derivation flows through the *same* machinery as any
inline condition — the Datalog-expressible parts (`type == "vehicle"`) become
store patterns, and the rest (`km > last_service_km + 20000`) becomes a Go-side
filter. Nothing downstream has to know a predicate was involved; the derived
fact reads like a stored one. (This is the same mechanism `define` + `is` use.)

Chained derivations inline transitively: `due_for_recall(v)` referencing
`overdue(v)` expands both, in order.

`talon explain` names the predicate in its WHY output:

```
WHY
  • satisfies derived overdue(v)
```

## Validation

- A `pred(v)` reference must resolve to a declared `derive` (else a compile
  error, with a did-you-mean suggestion).
- The derive dependency graph must be **acyclic**. A cycle is rejected at
  compile time (`recursive derive cycle through "x"`). For arity-1 predicates a
  cycle has no base case — it would never terminate — so rejecting it also
  subsumes the classical negation-through-recursion restriction.

## Worked example

Files: [`examples/vehicle_recall.talon`](../examples/vehicle_recall.talon) and
[`test/vehicle_recall.talon.test`](../test/vehicle_recall.talon.test).

A two-step chain runs with no host glue: `derive overdue → detect "Recall
candidates" → recommend "Book recall service"`.

| vehicle | km | last_service_km | model | overdue? | recalled model? | flagged |
|---|---|---|---|---|---|---|
| Van 1 | 80000 | 55000 | Transit | yes | yes | **yes** |
| Car 2 | 80000 | 55000 | Civic | yes | no | no |
| Van 3 | 70000 | 55000 | Sprinter | no | yes | no |

```
./talon build   examples/vehicle_recall.talon
./talon test    examples/vehicle_recall.talon test/vehicle_recall.talon.test
./talon explain examples/vehicle_recall.talon test/vehicle_recall.talon.test
```

## Scope (v1) and follow-ups

v1 is **arity-1** and **non-recursive**, resolved by inlining. This covers the
common case — a boolean predicate composed from a record's own attributes and
other predicates.

Tracked follow-ups (the machinery is already present — see the recursive
`category_tree` rule and `internal/factstore/rules.go`):

- **Recursion / arity-N** — binary+ predicates (`ancestor(X, Y)`) and
  self/mutually-recursive derivations compile to `factstore.Rule` +
  `RuleCall` and resolve to a fixpoint via the existing recursive resolver.
  Note that resolver supports pattern/equality/rule-call bodies (not
  arithmetic), so recursive bodies are relational.
- **Eager evaluation** — writing derived facts back to the store on assert,
  cheap once RETE (#89) lands. v1 is lazy (inline-per-query).
- **Alt B `produce {…}`** — derived facts that carry attributes (full EAV
  entities), as sketched in #91. v1 is boolean predicates only.
- **Negation through recursion** — forbidden by the acyclicity check in v1;
  well-founded/stable-model semantics are out of scope.

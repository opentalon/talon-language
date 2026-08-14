# Stratified negation (`not pred(v)`)

Any selector can negate a derived predicate with `not`. The negation reads as
the complement of the derivation — the vehicles that are *not* overdue, the
records that *don't* satisfy a policy — with no host glue and no second query.

```talon
derive overdue(v) {
  for records where type == "vehicle"
    and attr "km" > attr "last_service_km" + 20000
}

detect "Up to date" {
  for records where type == "vehicle"
    and status == "active"
    and not overdue(v)          // ← stratified negation-as-failure
  flag matching items
}
```

`not overdue(v)` means *negation as failure*: a record is "up to date" when
`overdue(v)` cannot be established for it. Worked example:
[`examples/negation.tln`](../examples/negation.tln) +
[`test/negation.tln.test`](../test/negation.tln.test).

## How it compiles

The planner splits a predicate body into the Datalog-expressible part (patterns,
membership) and the Go-side part (arithmetic over attributes). Negation is
lowered one of two ways depending on what the body needs:

- **Store-expressible body** (`not (attr "km" > 50000)`, a predicate whose body
  is all patterns/membership) → a set difference, `factstore.Not`, evaluated in
  the store:

  ```
  (not [?e :record/type "vehicle"] [?e :attr/km ?km] [(> ?km 50000)])
  ```

- **Mixed body** (the predicate also needs arithmetic over two attributes, like
  `overdue`) → the *whole* negation is evaluated per-row by the constraint
  evaluator, with the derived predicate inlined into its body first:

  ```
  step 1  FactQuery → candidates      [type == vehicle, status == active]
  step 2  Filter    → filtered        not (type == vehicle and km > last_service_km + 20000)
  ```

  The mixed case must stay a single unit: splitting it — Datalog part into the
  store's `Not`, arithmetic merged out as a positive filter — would flip the
  sign of the arithmetic and silently flag the wrong records. So a mixed-body
  negation is never split.

## Stratification — what is and isn't allowed

Negation is **stratified**: a derived predicate may not depend, even
transitively, on its own negation.

```talon
// Rejected at compile time — `loop` is true exactly when it is false.
derive loop(v) {
  for records where type == "x" and not loop(v)
}
// error: negation through recursion via "loop" is not stratifiable …
```

The validator builds a *signed* derive→derive dependency graph — each edge
tagged with the parity of the enclosing `not`s (`not not p` is positive again) —
and rejects any cycle that crosses a negative edge with a distinct
*not stratifiable* error. A purely positive cycle keeps the existing
"recursive derive cycle" error (arity-1 recursion has no base case in v1).

Negation *through* recursion has no meaning under this simple evaluation — it
needs **well-founded semantics** to resolve to a three-valued (true / false /
undefined) model. That is tracked in
[issue #170](https://github.com/opentalon/talon-language/issues/170); this page
covers only the stratified case, which is the common one.

## Scope

- `not pred(v)` where `pred`'s body is patterns, membership, string matches, and
  arithmetic comparisons — inlined and negated correctly, including transitively
  through chained derives.
- A negated body using a construct the per-row evaluator can't run standalone
  (e.g. an anomaly test) falls back to store-only negation; prefer keeping such
  conditions out of a negated predicate for now.

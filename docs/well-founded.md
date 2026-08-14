# Well-founded negation in the recursive resolver

Talon's recursive Datalog resolver (`internal/factstore/rules.go`) accepts a
negative body literal — a `Negation` clause — inside a `Rule`. When any rule in
a set carries one, the resolver stops using top-down tabling (which has no
order-independent reading under negation) and instead computes the set's
**well-founded model**: a unique three-valued interpretation where every ground
atom is *true*, *false*, or *undefined*.

This is the principled answer to *negation through recursion* — the case
stratified negation ([docs/negation.md](./negation.md)) rejects. The canonical
example is the game of positions:

```
win(X) :- move(X, Y), not win(Y)
```

A position is winning if some move leads to a non-winning position.

| graph | result |
|---|---|
| `a → b`, `b` terminal | `win(b)` **false** (no move), `win(a)` **true** |
| `a ⇄ b` (2-cycle, a draw) | `win(a)`, `win(b)` both **undefined** |

The draw is exactly where well-founded semantics earns its keep: naive
evaluation would loop or pick an arbitrary answer; the well-founded model says
*undefined* and means it.

## How it works

1. **Ground.** Each rule is instantiated over the store: its `Pattern`/`=`
   literals generate variable bindings, and every binding yields a ground rule —
   a head atom, its positive rule-call dependencies, and its negated ones.
2. **Alternating fixpoint** (Van Gelder). Define `A(S)` as the least set of
   atoms derivable when each `not a` is assumed to hold iff `a ∉ S`. `A` is
   antitone, so `A²` is monotone; its least fixpoint from `∅` is the
   well-founded **true** set. `A(true)` then yields *true ∪ undefined*, so any
   atom outside it is **false**, and the gap between the two sets is
   **undefined**.

Query answering returns only **true** atoms — a query yields definite answers,
so an undefined atom is not reported (just as a false one isn't). The three-valued
model is available to callers that need to distinguish the two.

## Scope and safety

- **Range restriction.** Every variable in a rule's head, positive rule-calls,
  and negated rule-calls must be bound by a `Pattern` or `=` literal in the same
  body — the same anchoring the planner already emits (see the `category_tree`
  rule). Non-ground instances are dropped.
- **No grammar surface yet.** `Negation` is constructed directly, like the
  internal recursive `category_tree` rule — there is no `.tln` syntax for
  recursive/negated rules yet (that rides with self-hosting,
  [#13](https://github.com/opentalon/talon-language/issues/13)).
- **Positive recursion stays on the top-down resolver.** A negation-free rule
  set (e.g. transitive closure) keeps the existing tabled evaluation unchanged;
  only negation-bearing sets take the well-founded path.
- **Stable-model / ASP semantics** (zero-or-many models) remain out of core —
  proposed as an external plugin,
  [#171](https://github.com/opentalon/talon-language/issues/171).

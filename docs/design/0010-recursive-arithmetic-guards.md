# ADR 0010 — Comparison/arithmetic guards in recursive rule bodies

Status: accepted

## Context

tln's recursive resolver (`internal/factstore/rules.go`, `wellfounded.go`)
evaluates `factstore.Rule` bodies made of `Pattern`, `RuleCall`, `Negation`, and
`Predicate` clauses. Until now the recursive path handled `Predicate` **only for
`=`/`==`** — every other operator dead-ended the derivation:

```go
case *Predicate:
    if c.Op != "=" && c.Op != "==" {
        return   // a `<`, `>`, `!=`, or string test in a recursive body yielded nothing
    }
```

So a rule like "reachable within a weight cap" or "path while `rank < limit`"
could not be expressed as a native recursive rule; it had to fall back to a
Go-side filter or an external engine. This is the first concrete gap surfaced by
the Prolog-porting work ([tln-prolog](https://github.com/opentalon/tln-prolog)):
most arithmetic in real recursive Prolog is **guards**, not term construction.

## Decision

Allow every non-`=` `Predicate` — comparisons (`< <= > >= !=`), string tests
(`starts_with`/`ends_with`/`contains`), and membership (`in`/`not_in`) — as a
**guard** inside a recursive rule body, on both the top-down resolver
(`enumerate`) and the well-founded evaluator (`enumGen`). The evaluation reuses
the existing `matchPredicate`, so query-time and rule-time predicate semantics
are identical.

```go
case *Predicate:
    switch c.Op {
    case "=", "==":
        // unify-or-check: may BIND an unbound side (unchanged)
    default:
        if matchPredicate(c, bindings) { /* continue */ }  // else prune
    }
```

## Why this is safe (termination preserved)

The distinction that matters is **guard vs. generator**:

- A **guard** evaluates over already-bound values and only *filters* — it never
  binds a fresh variable, so it introduces no value outside the EDB. The
  Herbrand base stays finite and the semi-naive / alternating fixpoint still
  converges. `matchPredicate` returns false when an operand is unbound
  (memory.go), which is exactly the **range-restriction / safety condition**: an
  unbounded guard operand prunes rather than enumerating.
- A **generator** — value-inventing arithmetic such as `N1 is N-1` that *binds*
  a fresh `N1` fed back into the recursion — is deliberately **out of scope**. It
  breaks the finite-model guarantee (`nat(0). nat(N1):-nat(N),N1 is N+1.` never
  closes) and is tracked separately; that class stays on the tln-prolog engine,
  whose depth bound is the pragmatic backstop.

Only `=`/`==` may bind, and only from an already-ground counterpart — no new
values are conjured, so the guarantee that made tln core deterministic and
terminating is intact.

## Scope

- **In:** comparison/string/membership guards over bound variables in recursive
  bodies, on both resolvers. Zero new AST types — only more `Op` values reach the
  resolver now.
- **Out (tracked separately):**
  - *Arithmetic-expression guards* (`N > K + 1`) need an expression term in
    `Predicate.Left/Right`; a small additive AST change, deferred.
  - *Value-inventing arithmetic* — engine-only (see above).
  - *Dynamic-name `RuleCall`* (the safe slice of `call/N`) — separate change.

## Consequences

More of the relational subset of Prolog lowers to native, terminating tln rules
instead of the embedded engine: bounded reachability, threshold/weight walks, and
string-filtered recursions. Proven by `internal/factstore/recursive_guard_test.go`
on both the plain (`enumerate`) and negation-bearing (`enumGen`) paths.

# FactStore — Talon's database abstraction

Talon's compiler and runtime never talk to a database directly. They talk
to the **FactStore** interface — a small Go contract any backend can
implement. Two implementations ship today (Datalevin over HTTP, in-process
MemoryStore); a future SQL or purpose-built backend plugs in without
touching the planner or executor. See [issue #14](https://github.com/opentalon/talon-language/issues/14).

## The interface

```go
type FactStore interface {
    Query(ctx context.Context, q Query) ([][]any, error)
    Assert(ctx context.Context, facts []Fact) error
}
```

Two methods. The planner emits structured `Query` values; backends
respond with rows. The planner does not emit Datalog text or SQL or any
backend-specific dialect — that is the backend's concern.

## The query model

A `Query` is the Go representation of what used to be Datalog text:

```go
type Query struct {
    Find  []string  // column names to return ("?e", "?km", …)
    Where []Clause  // patterns + predicates + or/not
}
```

`Clause` is implemented by four concrete types:

| Clause       | Semantics                                                       |
| ------------ | --------------------------------------------------------------- |
| `Pattern`    | An EAV match: entity-attribute-value triple. Variables bind, literals constrain, wildcards match anything. |
| `Predicate`  | A post-binding comparison (`<`, `<=`, `>`, `>=`, `==`, `!=`), string match (`starts_with`, `ends_with`, `contains`), or membership (`in`, `not_in`). |
| `Or`         | Disjunction of N clause lists. A row matches if any branch matches. |
| `Not`        | Negation. Row matches when the inner clauses fail to match.     |

`Term` carries either a variable reference (`?e`, `?km`) or a literal
value. Use `factstore.Var("name")` and `factstore.Lit(value)` at call
sites; the zero `Term{}` is a wildcard.

## Two backends today

### Datalevin (HTTP, production)

`*datalevin.Client` satisfies the interface. Its `Query` method renders
the structured `Query` to Clojure-flavoured Datalog using the shared
`Query.String()` renderer and sends it to a running `datalevin-server`.
Its `Assert` method groups facts by record-ID, infers a schema, and
commits one transaction.

### MemoryStore (in-process, default in REPL / tests / CI)

`*factstore.MemoryStore` is a Prolog-style in-memory triple store. It
holds facts in a `map[int]map[string]any` keyed by record-ID and
evaluates `Query` values directly with first-attempt variable binding +
short-circuit clause matching. No serialisation, no network, no JVM.

### `talon run --store memory`

```bash
talon run examples/cement_explain.tln --store memory --seed test/cement_explain.tln.test
# → 2 entities seeded, "Cement running low" matches 1 row, no Datalevin sidecar required.
```

`--store=datalevin` (the default) keeps the JVM path; `--store=memory`
switches to the in-process backend.

## Adding a backend

The contract is small enough that a new backend is a single file:

```go
type MyStore struct{ /* … */ }

func (s *MyStore) Query(ctx context.Context, q factstore.Query) ([][]any, error) {
    // walk q.Where, translate to your native query, execute, return rows.
    // q.String() gives you Datalog text for free if your backend speaks Datalog.
}

func (s *MyStore) Assert(ctx context.Context, facts []factstore.Fact) error {
    // upsert each fact (entity + attribute + value).
}
```

Wire it into `pkg/talon.WithFactStore(MyStore{...})` for embedded use,
or behind a new `--store` value in `cmd/talon/main.go`.

## Helpful affordances

- `Query.String()` renders any query as Datalog text (used by the
  Datalevin client, the test runner's trace output, and `talon build`'s
  step listing).
- `MemoryStore.Snapshot()` returns a deep copy of the entity map for
  display (used by the REPL's `:facts` command).
- `MemoryStore.Reset()` drops all facts; useful in tests and REPL
  `:clear facts`.

## What's deferred to follow-ups

The RFC sketches further capabilities — `QueryRecursive`, `QueryAsOf`,
explicit `Retract` — that aren't on the interface today because no
planner path needs them yet. They'll arrive alongside the language
features that consume them (recursive category-tree traversal,
time-travel queries, and explicit retraction respectively), rather than
sitting on the interface as undefined optional methods.

A SQL backend (Postgres / SQLite) and the eventual `talon-db` custom
engine are tracked under [#14](https://github.com/opentalon/talon-language/issues/14)
and [#4](https://github.com/opentalon/talon-language/issues/4).

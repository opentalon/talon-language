# `combine` block — language reference

The `combine` block is Talon's optimization construct. This page is the
authoritative reference for every clause it accepts and how clauses
combine. For "when do I use combine", see the
[optimizer overview](./README.md).

## Full grammar

```
CombineBlock = "combine" STRING "{" CombineBody "}"

CombineBody  = Selector
               { ClauseLine }

ClauseLine   = OptimizeClause
             | SelectClause
             | ConstraintClause
             | SequenceClause
             | CoordinatesClause
             | SolverClause
             | SeedClause
             | ReturnClause
             | LabelClause
             | PriorityClause

OptimizeClause     = ( "minimize" | "maximize" ) Expr
SelectClause       = "select" NUMBER [ "from" "records" ]
ConstraintClause   = "subject_to" Expr CompareOp Expr
SequenceClause     = "sequence"
CoordinatesClause  = "coordinates" Expr "," Expr
SolverClause       = "solver" ( "linear" )
SeedClause         = "seed" NUMBER
ReturnClause       = "return" IDENT { "," IDENT }
LabelClause        = "label" STRING
PriorityClause     = "priority" ( "LOW" | "MEDIUM" | "HIGH" | "CRITICAL" )
```

`for ... where ...` (the selector) must be the first clause. The
remaining clauses can appear in any order; the parser builds them into
a single `*ast.CombineBlock`.

## Clauses

### `for ... where ...`

Standard Talon selector. Filters the candidate population that the
optimizer operates on. The same syntax as in `detect`, `rule`, etc.

```talon
for records where type == "stock_item" and status == "active"
```

### `minimize` / `maximize` (one or more)

Each clause is one objective. The expression may be:

- A bare `attr "name"` — the per-row value (ranking mode).
- `total(attr "name")` — sum over selected subset (subset mode).
- `count(records)` — count of selected rows (subset mode).
- `count(attr "name")` — count of selected rows where attr is non-zero.
- `avg(attr "name")` — mean of attr over subset (GA only; rejected by
  ILP because avg is nonlinear in subset size).

Combine accepts multiple objectives (multi-objective Pareto). Required
unless `sequence` is set.

### `select K from records`

Switches the block to **subset mode**: pick exactly K records to
optimize over aggregates. Without this, the block ranks individual
records by their per-row objective values.

```talon
select 3 from records
```

The `from records` suffix is optional and stylistic; `select 3`
parses identically.

### `subject_to <agg> <op> <literal>`

A linear inequality constraint on the selected subset. The LHS must be
an aggregate expression (`total(...)`, `count(...)`, `avg(...)`); the
RHS must be a numeric literal. Comparison operators: `<=`, `<`, `>=`,
`>`, `==`, `!=`.

```talon
subject_to total(attr "reorder_cost") <= 5000
```

Requires subset mode (`select K`). Multiple `subject_to` clauses AND
together — every constraint must hold.

### `sequence` + `coordinates`

Opt in to ACO routing. Required together. Visits every candidate in
the optimal Hamiltonian tour minimizing total euclidean distance
computed from the two coordinate attrs.

```talon
sequence
coordinates attr "yard_x", attr "yard_y"
```

When `sequence` is set:
- `minimize` / `maximize` is rejected (objective is implicit).
- `select K` is rejected (visit every candidate).
- `subject_to` is rejected (not yet supported in sequence mode).
- `solver` is rejected (ACO is the only sequence backend).

### `solver linear`

Route to the ILP exact solver. Requires:
- Subset mode (`select K`).
- Exactly one objective.
- All objectives and constraints are linear sums (no `avg`).

The validator rejects invalid combinations at compile time.

```talon
solver linear
```

### `seed N`

Sets the random seed for stochastic backends (GA, ACO). Numeric
literal. With a fixed seed, runs are reproducible; without (or with
0), the runtime uses a stable default (also reproducible — pass any
non-zero seed for varied runs).

```talon
seed 42
```

Ignored by Pareto ranking and ILP, which are deterministic.

### `return <field>, <field>, ...`

The fields to surface in the result rows. Each field name should match
an attr referenced elsewhere in the block (objective, constraint, or
coordinate). For now, `return` is **advisory** — the result is keyed
by entity ID regardless. A future revision may filter the result rows
to just these fields.

```talon
return id, reorder_cost, downstream_blast_radius
```

### `label "<template>"`

Renders a per-entity label using `{item.name}` and `{attr.X}`
placeholders. Same template syntax as `detect`. Surfaces in
`Decision.Action` (the first line of `talon explain` output).

```talon
label "Reorder {item.name}: ${attr.reorder_cost}, blocks {attr.downstream_blast_radius} jobs"
```

### `priority LOW | MEDIUM | HIGH | CRITICAL`

Surfaces in `Decision.Priority`. Same semantics as other block types —
informational; sorting/filtering by priority is the caller's job.

```talon
priority HIGH
```

## Backend dispatch table

| Has `sequence` | Has `select K` | Has `solver linear` | Objectives | Backend |
|---|---|---|---|---|
| ✓ | — | — | — (implicit) | **ACO** |
| — | ✓ | ✓ | 1 | **ILP** |
| — | ✓ | — | ≥1 | **GA** |
| — | — | — | ≥1 | **Pareto** |

Any other combination is a validator error.

## Common errors

- *"combine X: solver linear requires `select K from records`"* — Add
  the `select` clause; ILP only solves subset problems.
- *"combine X: solver linear supports a single objective"* — Drop the
  extra `minimize` / `maximize` clauses, or drop `solver linear`.
- *"combine X: sequence mode requires `coordinates attr ..., attr ...`"* —
  ACO needs two coordinate attrs to compute distances.
- *"combine X: subject_to LHS must be an aggregate"* — Wrap the LHS in
  `total(...)`, `count(...)`, or `avg(...)`.
- *"avg() is nonlinear in subset size — drop solver linear"* — ILP
  doesn't support `avg`; either rewrite the constraint in terms of
  `total` and a count, or drop `solver linear`.

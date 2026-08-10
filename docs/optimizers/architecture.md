# Optimizers — code layout

This is a developer-facing map of where the optimizer code lives. For
"which optimizer should I use", see [README.md](./README.md) and the
per-backend worked examples in this folder.

## Package responsibilities

```
internal/optimize/        Pure algorithms — no Talon types
  ├── doc.go              Package preamble
  ├── types.go            Direction, Objective, Individual, Solution, Result
  ├── pareto.go           NSGA-II non-dominated sort + crowding distance
  ├── ga.go               Generic GA loop, constraint-dominance, GenerationStats
  ├── subset.go           SubsetProblem: 0/1 mask Individual with K-invariant
  ├── aco.go              Ant System / ACO, EuclideanDistanceMatrix helper
  ├── ilp.go              0/1 branch-and-bound, ILPProblem + LinearConstraint
  └── *_test.go           Knapsack / square-TSP / hand fixtures with known optima

internal/executor/        Talon → optimize bridge for production runs
  ├── optimize.go         optimize_pareto dispatch
  ├── optimize_ga.go      optimize_ga dispatch + aggregate closures
  ├── optimize_aco.go     optimize_aco dispatch
  └── optimize_ilp.go     optimize_ilp dispatch + linear coefficient extractor

internal/testrunner/      Talon → optimize bridge for `talon test` and `talon explain`
  ├── optimize.go         Pareto narrowing for in-memory entities
  ├── optimize_ga.go      GA narrowing
  └── optimize_aco_ilp.go ACO + ILP narrowings, Decision evidence builders

internal/planner/         Plan emission
  └── planner.go          planCombine{Pareto|GA|ACO|ILP} per syntax shape
```

## The dispatch chain

A combine block goes through this pipeline:

```
.tln source
   ↓ lexer       (recognizes tokens: select, subject_to, sequence, solver, ...)
   ↓ parser      (builds *ast.CombineBlock with Optimize, Constraints, Select, Solver, ...)
   ↓ validator   (rejects multi-objective ILP, missing coordinates in sequence mode, etc.)
   ↓ planner     (planCombine dispatches by syntax shape → emits Func{Pareto|GA|ACO|ILP})
   ↓ executor    (execComputation switch → execOptimize{Pareto|GA|ACO|ILP})
   ↓ optimize    (pure algorithm)
```

The same plan runs through the **testrunner** path when invoked by
`talon test` or `talon explain`, with in-memory `entity` fixtures
standing in for a real Datalevin store. The narrowing functions in
`internal/testrunner/optimize*.go` mirror the executor's dispatchers
on this lighter substrate.

## Adding a new optimizer

The pattern is well-trodden. To add (say) Simulated Annealing:

1. **Pick the language surface.** What clauses identify the user
   wants SA? (e.g., `solver annealing`, or auto-detect by shape.)
2. **Lexer + AST.** New tokens, fields on `CombineBlock`.
3. **Parser.** Recognize the clauses inside `parseCombine`.
4. **Validator.** Reject invalid combinations (e.g., SA on multi-objective).
5. **Algorithm in `internal/optimize/<name>.go`.** Pure functions; no
   Talon types in the package.
6. **Planner branch.** A `planCombine<Name>` that emits the right
   `FuncOptimize<Name>` constant in a `GoComputation` step.
7. **Executor dispatcher.** Add a case in `execComputation`'s switch
   and a file `internal/executor/optimize_<name>.go` translating row
   data into the algorithm's input.
8. **Testrunner narrowing.** Mirror the executor path against the
   in-memory `entity` store. Decision evidence + "why" lines.
9. **Tests.** Unit tests on the algorithm with known optima; planner
   test asserting the right dispatch; executor test against a fake
   `FactStore`; `examples/<name>.tln` + `test/<name>.tln.test`.
10. **Docs.** A `docs/optimizers/<name>.md` worked example following the
    pattern of `pareto-ranking.md`.

Roughly: ~150 LOC algorithm, ~150 LOC executor + testrunner glue,
~100 LOC tests, ~100 LOC docs. Reuse the NSGA-II kernel
(`fastNonDominatedSort`, `CrowdingDistance`) when the new algorithm
needs Pareto selection — it's exported test-helper-style for this.

## Explanation contract

Every optimizer is responsible for synthesizing per-entity Decision
evidence that `talon explain` can render. The pattern (see
`internal/testrunner/optimize_aco_ilp.go`) is:

```go
func <name>Evidence(entityID int, n <name>Narrowing) []explain.Fact { ... }
func <name>Why(entityID int, n <name>Narrowing) []string { ... }
```

The `Why` string should explain the decision in **population-relative**
terms — "selected in 67 of 100 subsets" or "stop 3 of 7 on the shortest
tour" — not per-row terms. That's what makes a `combine` Decision
fundamentally different from a `detect` Decision: it's never just "this
row matched the predicate."

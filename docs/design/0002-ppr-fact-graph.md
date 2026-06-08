# ADR-0002: Personalized PageRank Over The Fact Graph (`find related`)

## Status

Implemented.

## Context

Talon's `find similar` primitive (`internal/planner/planner.go:18`,
`internal/mlruntime/...`) ranks entities by pairwise feature distance —
cosine on hand-engineered vectors per ADR-0001. That's the right answer
for "give me records that look alike on attributes A, B, C," but a
known weakness: it can't follow multi-hop relations.

HippoRAG ([NeurIPS '24](https://arxiv.org/abs/2405.14831)) demonstrated
that Personalized PageRank (PPR) over a graph built from a corpus
recovers multi-hop relevance without per-query LLM inference. That
property aligns with Talon's deterministic, audit-first contract — the
graph build and the power iteration are pure functions of the input.

We want associative-memory retrieval inside Talon for use cases that
`find similar` cannot serve cleanly:

- "Find parts related to this part" via shared categories, suppliers,
  co-consumption history.
- "Find tickets adjacent to this incident" via shared customers,
  shared error codes, references.
- "Surface entities the system thinks are linked, even when no single
  attribute matches."

## Decision

Add a new primitive: **`find related "name" { ... }`** — a sibling to
`find similar`. Algorithm: Personalized PageRank, computed by
power iteration in pure Go over an entity-to-entity `GraphSnapshot`
projected from the FactStore.

Key design choices:

1. **Separate keyword from `find similar`.** Overloading the existing
   block would silently switch algorithms based on which parameters
   the user wrote, which violates ADR-0001's explainability contract.
2. **Pure Go, no graph library.** ADR-0001 forbids external ML/numeric
   deps; PPR is ~60 lines of Go (`internal/mlruntime/ppr.go`).
3. **Snapshot-based.** The primitive operates on an immutable
   `factstore.GraphSnapshot` value. Whoever builds the snapshot decides
   the policy (which attributes to include, hub caps, weights).
4. **Deterministic.** Same input → byte-identical output. No map
   iteration on the hot path. Sort by score desc, then node ID asc for
   tiebreaks.

## Algorithm: Power-Iteration PPR

```
Initialize:
  personalization[i] = 1/|seeds| for i in seeds, else 0
  rank = personalization
For up to max_iterations:
  next = (1-damping) * personalization
  for each edge (i -> j with weight w):
    next[j] += damping * rank[i] * w / row_sum(i)
  redistribute dangling-node mass uniformly to personalization
  if |next - rank|_1 < tolerance: converged
  rank = next
Return rank, sorted desc, top-K
```

Tunables (with defaults):

| Param            | Default | Range       | Effect                          |
|------------------|---------|-------------|---------------------------------|
| `damping`        | 0.85    | `[0, 1)`    | Higher → mass spreads further   |
| `tolerance`      | 1e-6    | `> 0`       | L1 convergence threshold        |
| `max_iterations` | 100     | `> 0`       | Hard cap; `converged=false`     |
| `top_k`          | 10      | `> 0`       | Result count after seed removal |
| `include_seeds`  | false   | bool        | Keep seed nodes in output       |

`damping == 1` is rejected: the random surfer never restarts, and the
iteration may not converge for disconnected graphs. The validator
catches this at compile time (`internal/validator/validator.go`).

## Graph Model

`factstore.GraphSnapshot` (see `internal/factstore/graph.go`):

- **Nodes** are entity IDs, lexicographically sorted.
- **Edges** are emitted by:
  1. Shared `(attribute, value)` pairs — two entities that hold the
     same value for the same attribute share an undirected edge.
  2. Entity-valued attributes — if entity `X` has attribute
     `assigned_to = Y` and `Y` is itself a node, emit a direct edge.
- **Weights** sum across shared-attribute buckets. Per-attribute
  weighting via `SnapshotOptions.AttributeWeights` is reserved for v2;
  v1 weights are uniform 1.0.
- **Hub cap.** `SnapshotOptions.MaxBucketSize` (default 500) drops any
  `(attribute, value)` bucket above the cap. Without this a
  high-cardinality attribute like `status="active"` shared by 10k
  entities would emit ~5×10⁷ edges and blow the build time/memory.

Storage is CSR adjacency (`EdgesFrom`, `EdgeWeights`). Build is
O(triples × avg-bucket-size); query is O(iterations × edges).

## Language Surface

```talon
// Standalone: rank stock items most associated with part 808.
find related "Co-consumed parts" {
  for records where type == "stock_item"
  seeds [808]
  top_k 5
  damping 0.85
  label "{item.name}: ppr {score}"
  priority MEDIUM
}

// Nested in detect: use PPR-derived neighbourhood as part of a rule.
detect "Investigate related parts" {
  for records where type == "stock_item" and status == "active"
  flag matching items
  find related to attr "id" top_k 5 damping 0.85
}
```

A `RelatedBlock` is required to provide either `to <expr>` (single
seed expression — typically `attr "id"`, which broadcasts across every
candidate row) or `seeds [<list>]`. The validator rejects blocks that
omit both, and rejects out-of-range damping/top_k.

## Output

Results bind to `related_records`. Each row carries:

- `Result.EntityID` — integer ID parsed from the entity string, or
  hashed when the entity is non-numeric. The original string is
  preserved in `Explanation.Inputs["entity"]`.
- `Result.Value` — `float64` PPR score.
- `Explanation` — `Primitive: "ppr_topk"`, `Rules: [{Attr:"ppr_score",
  Op:"topk", Value:top_k, Observed:score}]`, plus
  `Inputs: {seeds, damping, tolerance, iterations, converged,
  graph_nodes, graph_edges, graph_version, score, rank}`.

## Caching

`PersonalizedPageRank` carries a small versioned cache (default 4
slots, LRU-ish): `SnapshotForCache(key, snap)` to store,
`CachedSnapshot(key, version)` to retrieve. Cache hit requires
`snap.Version == requested_version`. The Version counter is opaque to
the primitive — whoever builds the snapshot supplies a monotonically
increasing integer (typically tied to FactStore generation).

## Determinism Guarantees

| Guarantee                                  | How                                       |
|--------------------------------------------|-------------------------------------------|
| Same input → byte-identical output         | Sorted node order; arrays only            |
| Tiebreak stable                            | `score desc, then Nodes[i] asc`           |
| Insertion order independent                | `BuildSnapshotFromTriples` sorts nodes    |
| Convergence reproducible                   | No randomness anywhere                    |

Tested in `internal/mlruntime/ppr_test.go::TestPPRDeterministic*`.

## Risks / Open Questions

- **Hub explosion.** Mitigated by `MaxBucketSize`. Builders that pass
  unfiltered FactStore data should set per-attribute weights or
  exclude lists.
- **Cold-start.** First call on a large FactStore pays the full
  snapshot build. A `talon graph warm` CLI is out of scope for v1.
- **Performance ceiling.** Power iteration is O(iters × edges).
  Interactive query latency holds to ~10M edges; beyond that, switch
  to push-based PPR via the `mlruntime.Backend` escape hatch reserved
  in ADR-0001.
- **Numeric vs string IDs.** Talon test rows use integer IDs; the
  primitive parses entity strings back with a hash fallback. The
  original string always lives in `Explanation.Inputs["entity"]` so
  audit trails round-trip cleanly.
- **`flag matching items` semantics.** A ranked top-K is not a binary
  flag. Current rule: any non-zero PPR score counts as "flagged" for
  the purpose of intersecting with `detect`'s flagged set
  (`internal/executor/executor.go::extractFlaggedIDs`).

## Files

- `internal/factstore/graph.go` — `GraphSnapshot`, `BuildSnapshotFromTriples`.
- `internal/mlruntime/ppr.go` — `PersonalizedPageRank` primitive.
- `internal/lexer/lexer.go` — `TokenRelated` (`related` keyword).
- `internal/ast/ast.go` — `RelatedBlock`, `RelatedClause`.
- `internal/parser/parser.go` — `parseRelatedBlock`,
  `parseNestedRelatedClause`, `find` dispatch.
- `internal/validator/validator.go` — `RelatedBlock` completeness check.
- `internal/planner/planner.go` — `FuncPPRTopK`, `GraphSnapshot` plan
  step, `planRelatedBlock`.
- `internal/executor/executor.go` — `execGraphSnapshot`,
  `resolveMLParams`, float-aware `extractFlaggedIDs`.
- `internal/testrunner/testrunner.go` — in-memory `runPPR`,
  `buildGraphFromEntities`.
- `examples/related_parts.talon`, `test/related_parts.talon.test` — E2E example.

## References

- ADR-0001 — ML runtime strategy and the deterministic-Go ground rules.
- HippoRAG (Gutiérrez et al., NeurIPS '24).
- HippoRAG 2 (Gutiérrez et al., ICML '25) —
  https://arxiv.org/abs/2502.14802.

# ADR-0001: ML Runtime Strategy

## Status

Proposed — gates [opentalon/talon-language#11](https://github.com/opentalon/talon-language/issues/11).

## Context

Talon's pitch (`README.md:21`, `README.md:159`):

> **Deterministic** — detections are explainable and auditable.
> Every prediction is explainable.

That promise is the product. Whatever ML runtime we pick has to make
"the user can read the reasoning" mechanically true — not aspirational.

Today, ML keywords are wired through lexer, AST, and planner but the
runtime is a stub:

- **Lexer** (`internal/lexer/lexer.go:104`, `internal/lexer/lexer.go:213`):
  tokens for all 7 ML keywords (`predict`, `forecast`, `classify`,
  `cluster`, `find similar`, `is anomaly`, `learned_threshold`).
- **AST** (`internal/ast/ast.go:90-139`, clauses at lines 373-408):
  6 of 7 have AST nodes. `LearnedThresholdClause` is **missing** —
  token exists, parser/AST/planner do not.
- **Planner** (`internal/planner/planner.go:14-19`): 6 function-name
  constants (`AnomalyZscore`, `PredictDecisionTree`,
  `ForecastExpSmoothing`, `ClusterDBSCAN`, `SimilarityCosine`,
  `ClassifyKNN`, plus `RenderTemplate`). Each ML block emits a
  `GoComputation` step (`planner.go:248-352`).
- **Executor** (`internal/executor/executor.go:128-137`): all 6 ML
  functions short-circuit to an empty stub — no values, no
  explanations.

Phase 3a (talon-db, persistent FactStore) lands in the same calendar
year, so model persistence is a real adjacent concern, not hypothetical.

## Decision

**Built-in Go for all 7 primitives. No ONNX. No Python sidecar.**

Algorithms chosen for explainability first, accuracy second
(table in §"Per-Primitive Algorithm Choices"). Every primitive
returns a structured `Explanation` alongside its value.

One escape hatch: a `Backend` interface so a later
`predict_onnx` or `similarity_embeddings` backend can be swapped in
per-tenant without touching Talon source (see §"Future Backend Swap").

## Consequences

**Bought:**
- Single deployable Go binary. No CGo, no GPU build matrix, no
  Python runtime in prod.
- Determinism is achievable — pure Go, documented tiebreaks, no
  hidden floats from CUDA kernels.
- Explanations are first-class outputs, not post-hoc reconstructions.
- Tests stay fast and hermetic; no model file fixtures.

**Paid:**
- Accuracy ceiling lower than sklearn/XGBoost on hard tabular and any
  free-text problem. See §"Accuracy Trade-offs".
- ~3000-3500 LOC of ML code to own, including a hand-rolled CART tree
  (gonum has no production decision-tree implementation).
- TF-IDF/k-NN dies on synonym-heavy text. First customer with a real
  ticket corpus exposes this — `find similar` is the canary.

## Rejected Alternatives

| Option | Why rejected |
|---|---|
| ONNX Runtime via Go bindings | Adds CGo, GPU build matrix, opaque models. Breaks the "user reads the reasoning" pitch — an ONNX graph is not auditable in the way Talon claims. |
| Python sidecar (gRPC) | Two runtimes to ship and version. Two failure modes. Multi-tenant cost balloons. Loses the "one binary" property. |
| Pure Datalog/SQL aggregations | Sufficient for `learned_threshold` and `is anomaly`, insufficient for `forecast`/`predict`/`cluster`/`classify`/`find similar`. Inconsistent runtime story. |
| Vendor a Go ML library (gorgonia, golearn) | gorgonia is dataflow/autograd, overkill. golearn is unmaintained (last commit pre-2023). Building 7 small, audited primitives is less risk than vendoring an abandoned dep. |

## Explainability Contract

Load-bearing piece. Every primitive returns `(value, Explanation)` so
labels like `"failure risk because operating_hours > 2000 AND
repair_count > 3"` are computable at the planner/executor layer, and
`talon trace` (currently a stub at `cmd/talon/main.go:36-43`) has a
real shape to render.

```go
// internal/mlruntime/explanation.go

// Explanation is the auditable trace of one ML primitive invocation.
// JSON-serialisable for `talon trace` and audit logs.
type Explanation struct {
    Primitive  string         // "predict_decision_tree", etc.
    EntityID   int            // who this prediction is about
    Inputs     map[string]any // feature name → observed value
    Rules      []Rule         // decision path (tree branches, IQR bounds…)
    Confidence float64        // 0..1
    Threshold  *Threshold     // for learned_threshold / anomaly
}

// Rule is one human-readable predicate that fired.
// "operating_hours > 2000" or "z_score > 2.5".
type Rule struct {
    Attr     string // "operating_hours"
    Op       string // ">", "in_range", "matches_cluster"
    Value    any    // threshold value or reference value
    Observed any    // what the entity actually had
}

type Threshold struct {
    Method string  // "percentile_p95", "iqr_upper", "mad_3sigma"
    Value  float64
    Sample int     // window size used
}

// Primitive is implemented once per keyword.
type Primitive interface {
    Name() string
    Compute(ctx context.Context, in Input) ([]Result, error)
}

type Input struct {
    Rows   [][]any        // result of upstream DatalevinQuery
    Schema map[string]int // column name → index
    Params map[string]any // from GoComputation.Params
}

type Result struct {
    EntityID    int
    Value       any         // bool for is anomaly, float for predict, etc.
    Explanation Explanation
}
```

**Wiring:** results merge into `vars[step.Into]` so the existing
`render_template` step (`planner.go:RenderTemplate`) can interpolate
`{explanation.rules}`, `{explanation.confidence}`, etc. into detect
labels.

## Per-Primitive Algorithm Choices

| Keyword | Algorithm | Why explainable | Complexity | LOC est. |
|---|---|---|---|---|
| `learned_threshold` | Sample percentile / mean+stddev / IQR fence over historical query result | Threshold value + window size *is* the explanation | **S** | ~150 |
| `is anomaly` | Z-score OR IQR OR MAD (config flag) | "obs=125, mean=80, stddev=15, z=3.0 > 2.5" | **S** | ~250 |
| `forecast` | Single-exp / Holt linear smoothing → solve for t when y(t)=threshold | "trend=-0.8/day, current=45, hits 0 at day 56" | **M** | ~400 |
| `predict` | CART decision tree (hand-rolled, Gini, depth cap) | Root-to-leaf path = the explanation literally | **L** | ~800 |
| `classify` | k-NN over TF-IDF / hashed n-gram vectors | "matched 'engine fault' (sim=0.83) — 4 of 5 neighbours are class X" | **L** | ~700 |
| `cluster by` | DBSCAN over numeric attrs (cosine or euclidean) | "cluster #3 centroid=[...], 12 members, eps=0.4" | **M** | ~500 |
| `find similar` | Cosine similarity over the same feature vectors as `classify` | "sim=0.91 on [feature_a, feature_b]" | **M** | ~300 |

Total: ~3000-3500 LOC + tests, roughly one focused engineer-month.

## Planner Integration: `MLComputation` Step (vs Overloading `GoComputation`)

Two options were on the table:

**A. Keep `GoComputation`, no new step.** Add `Explanation` as a
well-known key in `GoComputation.Params` outputs.

**B. Add `MLComputation` step.** Distinct struct, strongly-typed
`Explanation`, validator enforces label templates referencing
`{explanation.*}` only follow an `MLComputation`.

**Decision: B.** The headline pitch is explainability. The way to
make a contract real in Go is a type, not a convention. Cost is ~30
LOC of plan-step plumbing plus one switch case in
`executor.execStep` (`executor.go:81-92`). Cheap; pays for itself the
first time `talon trace` needs to enumerate predictions.

## Accuracy Trade-offs

Explainability caps accuracy. Be honest about where.

| Primitive | Built-in ceiling | What we lose |
|---|---|---|
| `predict` | CART single tree, depth ~6 | No gradient boosting, no random forest. Tabular accuracy typically ~10-15pp below XGBoost. |
| `classify` | k-NN on TF-IDF | No semantic similarity. "engine fault" and "motor failure" look unrelated. |
| `find similar` | Cosine on hand-engineered features | Same as classify. Hard ceiling without embeddings. |
| `forecast` | Single/double exp smoothing | No seasonality, no exogenous regressors. Fine for monotonic stock-out, weak for weekly demand. |

**Pitch defence:** Talon doesn't compete with sklearn. It competes
with "the engineer hand-writing a threshold check in TypeScript."
Against that baseline, CART + clear reasons + audit trail wins.

## Future Backend Swap

Three triggers will likely force us into an ONNX or embedding-based
backend for at least one primitive, in likely order:

1. **`find similar` on free text.** TF-IDF dies on synonym-heavy
   domains (maintenance tickets, medical notes). First customer with
   a real text corpus exposes this.
2. **`classify` with >5 classes and noisy features.** k-NN's curse
   of dimensionality. Around the same time as (1).
3. **`predict` where domain experts say "my gut beats the tree".**
   Happens with >20 features and subtle interactions. Less urgent —
   v1 still ships usable.

Escape hatch (cheap to design now, cheap to defer building):

```go
// internal/mlruntime/registry.go
type Backend interface {
    Compute(ctx context.Context, in Input) ([]Result, error)
}

var registry = map[string]Backend{
    "predict_decision_tree":  &PredictTreeBackend{},
    "predict_onnx":           nil, // future
    "similarity_cosine":      &CosineBackend{},
    "similarity_embeddings":  nil, // future
}
```

Talon source remains `predict "X" { ... }`; planner picks backend
based on tenant config. **No language change.** ~half a day to wire
when the trigger arrives.

## Determinism

"Deterministic" is in the pitch (`README.md:21`). Easy to break in
ML code without discipline. Documented tiebreaks per primitive:

- **k-NN ties** (`classify`, `find similar`): break by lowest entity
  ID, then lowest class ID. Document in `classify.go`.
- **DBSCAN equal-distance neighbours**: stable sort by entity ID
  before density check.
- **CART feature-split ties** (`predict`): pick the feature with the
  lowest column index — i.e., the order they appear in the input
  schema.
- **Forecast threshold-crossing on flat series**: return `+Inf`
  sentinel, not `NaN`.

Every primitive's golden test asserts the same input → same
`Explanation` JSON byte-for-byte.

## Open Questions

These do not block the ADR but block individual milestones.

1. **`learned_threshold` parser/AST gap.** Lexer emits the token
   (`lexer.go:104,213`); parser, AST, and planner have nothing.
   M3 starts with ~100 LOC of parser + AST work *before* the
   primitive itself. Worth confirming in code on day 1 of M3 — gap
   could be wider than estimated.
2. **Model persistence.** `trained_on` implies a fit step. Where
   does a trained CART tree live? Options:
   (a) Datalevin as a blob attribute on a `:model` entity;
   (b) side file under `~/.talon/models/`;
   (c) retrain every run (ok for small data, prohibitive at scale).
   Decision deferred to ADR-0003 (talon-db storage).
3. **Re-train cadence.** Every run, scheduled, on demand?
   Affects how `trained_on` desugars in the planner.
4. **Embedding strategy for `classify`/`find similar` without a
   neural model.** Hashed bag-of-words? TF-IDF with what tokenizer?
   What's the documented boundary at which we tell the user
   "use the ONNX backend"?
5. **CART training library.** gonum has no decision tree.
   golearn is unmaintained. Plan: vendor a small hand-rolled CART
   (~400 LOC) — auditable, tests over the algorithm itself.
6. **Testrunner ML support.** `testrunner.go:62` only evaluates the
   first `DatalevinQuery` step. To assert on predictions in
   `.talon.test` files, `runOne` needs to walk the full step list
   through `mlruntime.Registry`. ~150 LOC. Counts toward M2 cost.

## Milestones (Tracking)

Frozen at the time of writing for cross-reference. Authoritative
schedule lives in `docs/design/IMPLEMENTATION_PLAN.md`.

| Milestone | Exit criteria |
|---|---|
| **M1** | This ADR merged, `Explanation` interface frozen |
| **M2** | `MLComputation` step in planner + executor + testrunner |
| **M3** | `learned_threshold` + `is anomaly` shipped with explanations |
| **M4** | `forecast` shipped, stock-out example produces real `days_until` |
| **M5** | `predict` shipped, failure-risk example produces real decision path |
| **M6** | `classify` + `find similar` shipped |
| **M7** | `cluster by` shipped — closes #11 |

Each milestone is **not done** until (a) tests green, (b) one
example in `examples/` exercises it, (c) `talon trace` shows the
explanation. Without (c) the explainability claim is vapour.

# Talon Phase 2 — Implementation Plan

Tracks: opentalon/talon-language#11 (ML Primitives Runtime) and adjacent prerequisites.

## 1. Dogfood Setup (macOS)

The fleet example exercises `detect` and `rule` blocks against a live Datalevin sidecar. End-to-end smoke target: `talon run examples/fleet_maintenance.talon --seed test/fleet_maintenance.talon.test` returns 2 overdue vehicles.

### 1a. Prerequisites

```bash
# Go (already required for the compiler)
brew install go              # need 1.24+, see go.mod:3

# JVM + Clojure CLI (the server is JVM-Clojure, not the native dtlv binary)
brew install --cask temurin  # JDK 21 — matches CI (.github/workflows/ci.yml:77)
brew install clojure/tools/clojure
```

Note: `brew install datalevin` does **not** exist. The README mentions `brew install datalevin` near `examples/fleet_maintenance.talon:8` but Homebrew has no such formula. The two real paths are:

- **JVM server (what `talon run` actually uses)** — `datalevin-server/` is a Clojure ring app pulling `datalevin/datalevin 0.10.7` from Maven (`datalevin-server/deps.edn:3`). No separate install — `clj -M:run` pulls deps.
- **Native `dtlv` CLI (only needed for ad-hoc REPL via `examples/test_datalevin.clj`)** — download release zip manually, as CI does (`.github/workflows/ci.yml:53-58`).

Action: update the `fleet_maintenance.talon` comment block — the brew instruction is misleading.

### 1b. Boot the sidecar

```bash
# Terminal 1 — clean DB + start server
rm -rf /tmp/talon-datalevin
cd /Users/zh/dev/projects/opentalon-ai/talon-language/datalevin-server
clojure -M:run                # binds 0.0.0.0:8898, see server.clj:75
```

Server logs `datalevin-server listening on port 8898`. Verify in Terminal 2:

```bash
curl -s http://localhost:8898/health   # → {"status":"ok"}
```

### 1c. Build and run Talon

```bash
cd /Users/zh/dev/projects/opentalon-ai/talon-language
go build -o talon ./cmd/talon

# Compile-only sanity
./talon build examples/fleet_maintenance.talon

# Seeded end-to-end run
./talon run examples/fleet_maintenance.talon \
  --seed test/fleet_maintenance.talon.test
```

### 1d. Expected output

From `cmd/talon/main.go:296-332`, output is per-block flagged rows with resolved `:attr/name`. The CI assertion (`ci.yml:102`) is `Service overdue 2 row(s)` — Truck A (501) + Car C (505). Tests for `Unusual consumption` and `Parts stock-out` will show ML-keyword blocks running but the `GoComputation` step is a stub (`executor.go:128-137`) so labels/anomaly scores will be empty. That stub is exactly the gap #11 fills.

### 1e. Likely paper-cuts

| Symptom | Cause | Fix |
|---|---|---|
| `cannot reach datalevin-server` | Java not on path or `clj` not installed | `java -version`, re-run brew commands |
| `seed` succeeds but 0 rows flagged | Stale DB on disk with old schema | `rm -rf /tmp/talon-datalevin`, restart server |
| `:where]` empty query short-circuits to `[][]any{}` | `executor.go:96` heuristic — fires when `calculate` has no `where` | Cosmetic; won't break smoke test |
| `Cluster/Classify/Similar` blocks emit but return stub | Expected — see §3 below | n/a |

---

## 2. ML Runtime Strategy Design Doc

### 2a. File path

Repo has no `docs/` tree yet. Create one — `docs/design/` is the conventional Go layout. Proposed path:

```
docs/design/0001-ml-runtime-strategy.md
```

ADR-style numbering (0001, 0002…) sets up future `0002-explainability-contract.md`, `0003-talon-db-storage.md` for Phase 3a.

### 2b. Section structure

```markdown
# ADR-0001: ML Runtime Strategy

## Status
Proposed — gates issue #11.

## Context
- 7 ML keywords already in lexer/AST/planner: internal/lexer/lexer.go:104,213
  and internal/ast/ast.go:90-139. Planner emits placeholder GoComputation steps
  (planner.go:160-167, 248-352) but executor stubs them (executor.go:128-137).
- README pitch: "Every prediction is explainable… the user can read the
  reasoning." (README.md:159)
- Phase 3a (talon-db) lands inside the same year.

## Decision
Built-in Go for all 7 primitives. No ONNX. No Python sidecar.

## Consequences
- Single deployable Go binary, deterministic, no GPU/CUDA story.
- Accuracy capped at interpretable algorithms (decision trees not random
  forests, k-NN not embeddings).
- One escape hatch: `MLBackend` interface so a later `onnx_backend` can be
  swapped in for `find similar` if domain data forces it.

## Rejected Alternatives
| Option | Why rejected |
|---|---|
| ONNX Runtime via Go bindings | Adds CGo, GPU build matrix, opaque models — breaks "user reads the reasoning". |
| Python sidecar (gRPC) | Two runtimes to ship, two failure modes, multi-tenant cost. |
| Pure Datalog/SQL aggregations | Insufficient for forecast/cluster/classify. |

## Explainability Contract
[Go interfaces — see 2c]

## Per-Primitive Algorithm Choices
[Table — see section 3]

## Open Questions
- Where do trained models persist? (`FactStore` as blobs? separate file?)
- How does `trained_on` re-train — every run, scheduled, on demand?
- Embedding strategy for `classify`/`find similar` without a neural model:
  hashed bag-of-words? TF-IDF? Document the boundary.
- Decision tree training library: gonum/learn anemic. Vendor a small CART
  implementation (~400 LOC) or write one.
```

### 2c. Explainability contract — Go sketch

Load-bearing piece. Every primitive returns `(value, Explanation)` so labels like `"failure risk because operating_hours > 2000 AND repair_count > 3"` are computable at the planner/executor layer.

```go
// internal/mlruntime/explanation.go (proposed)

// Explanation is the auditable trace of one ML primitive invocation.
// JSON-serialisable for `talon trace` and audit logs.
type Explanation struct {
    Primitive  string              // "predict_decision_tree", etc.
    EntityID   int                 // who this prediction is about
    Inputs     map[string]any      // feature name → observed value
    Rules      []Rule              // decision path (tree branches, IQR bounds…)
    Confidence float64             // 0..1
    Threshold  *Threshold          // for learned_threshold / anomaly
}

// Rule is one human-readable predicate that fired.
// "operating_hours > 2000" or "z_score > 2.5".
type Rule struct {
    Attr     string  // "operating_hours"
    Op       string  // ">", "in_range", "matches_cluster"
    Value    any     // threshold value or reference value
    Observed any     // what the entity actually had
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
    Rows   [][]any            // result of upstream DatalevinQuery
    Schema map[string]int     // column name → index
    Params map[string]any     // from GoComputation.Params
}

type Result struct {
    EntityID    int
    Value       any           // bool for is anomaly, float for predict, etc.
    Explanation Explanation
}
```

Bake `Explanation` into `planner.GoComputation` results — extend `executor.StepResult.Output` shape so `render_template` can interpolate `{explanation.rules}` into labels.

---

## 3. Issue #11 Implementation Breakdown

### 3a. Algorithm choices and complexity

| Keyword | Algorithm | Why explainable | Complexity | LOC est. |
|---|---|---|---|---|
| `learned_threshold` | Sample percentile / mean+stddev / IQR fence over historical query result | Threshold value + window size is the entire explanation | **S** | ~150 |
| `is anomaly` | Z-score OR IQR OR MAD (config flag) | "obs=125, mean=80, stddev=15, z=3.0 > 2.5" | **S** | ~250 |
| `forecast` | Single-exp / Holt linear smoothing → solve for t when y(t)=threshold | "trend=-0.8/day, current=45, hits 0 at day 56" | **M** | ~400 |
| `predict` | CART decision tree (hand-rolled, Gini, depth cap) | Root-to-leaf path = the explanation literally | **L** | ~800 |
| `classify` | k-NN over TF-IDF / hashed n-gram vectors | "matched 'engine fault' (sim=0.83) — 4 of 5 neighbours are class X" | **L** | ~700 |
| `cluster by` | DBSCAN over numeric attrs (cosine or euclidean) | "cluster #3 centroid=[...], 12 members, eps=0.4" | **M** | ~500 |
| `find similar` | Cosine similarity over the same feature vectors as `classify` | "sim=0.91 on [feature_a, feature_b]" | **M** | ~300 |

Total: 3000–3500 LOC + tests. Roughly one focused engineer-month.

### 3b. Planner: does ML need a new step kind?

Current step interface (`planner.go:31-59`): `DatalevinQuery | GoComputation | Filter`. ML already runs through `GoComputation` with a `Function` string constant. Two options:

**Option A — keep `GoComputation`, no new step.** Add `Explanation` as a well-known key in `GoComputation.Params` outputs.
- Pro: zero planner churn, all 7 ML funcs already named (`planner.go:14-19`).
- Con: explanation is conventionally untyped — easy to forget, hard to lint.

**Option B — add `MLComputation` step.** Distinct struct, strongly-typed `Explanation`, validator can enforce that label templates referencing `{explanation.*}` only follow an `MLComputation`.
- Pro: explainability contract is structural, not nominal. Tooling (`talon trace`) gets a free hook.
- Con: planner and executor get a parallel branch.

**Recommendation: Option B.** Headline product pitch is "explainable"; way to make a contract real in Go = a type. Adds one switch case to `executor.execStep` (`executor.go:81-92`) and ~30 LOC of plan-step plumbing.

### 3c. Files to touch

```
internal/mlruntime/
  explanation.go        NEW   types: Explanation, Rule, Threshold, Primitive, Input, Result
  registry.go           NEW   map[string]Primitive — replaces stub MLRuntime struct
  threshold.go          NEW   learned_threshold + helpers (percentile, IQR fence)
  anomaly.go            NEW   z-score + IQR + MAD detectors
  forecast.go           NEW   exp smoothing + threshold-crossing solver
  predict.go            NEW   CART decision tree (train + predict + path trace)
  classify.go           NEW   TF-IDF + k-NN
  cluster.go            NEW   DBSCAN
  similar.go            NEW   cosine similarity
  *_test.go             NEW   table-driven unit tests for each, plus golden Explanation JSON
  mlruntime.go          MOD   wire registry, drop the stub

internal/planner/
  planner.go            MOD   add MLComputation step type; planner emits it for ML funcs
  planner_test.go       MOD   assert new step shape per ML block

internal/executor/
  executor.go           MOD   add execMLComputation; call mlruntime.Registry.Get(fn).Compute()
                              merge Explanation into vars[step.Into] so render_template sees it

internal/testrunner/
  testrunner.go         MOD   evaluate ML steps in-memory (mirror executor path so .test
                              files validate ML behaviour without Datalevin)

internal/ast/
  ast.go                LOOK  no schema changes expected — all 7 ML blocks already exist
                              (ast.go:90-139). Confirm learned_threshold wired —
                              lexer has token (lexer.go:104) but may lack
                              LearnedThresholdClause; could need ~100 LOC in parser/ast.

examples/
  fleet_maintenance.talon  MOD  add a predict block exercising the new path
test/
  fleet_maintenance.talon.test  MOD  add cases for new predict block
```

The `learned_threshold` gap is worth confirming day 1. Token exists; AST/parser may not have a clause node.

### 3d. Test-first checklist

Each row = one `.talon.test` case. Happy + edge + explanation assertion. Testrunner already handles `flagged` / `not flagged` (`testrunner.go:341-358`); extend to assert on explanation fields.

**`learned_threshold`**
- p95 over 100 samples returns correct value
- threshold over empty sample set → diagnostic (not silent zero)
- threshold over constant series → stddev=0, returns mean
- explanation contains `Method` and `Sample`

**`is anomaly`**
- single clear outlier (z=4) is flagged, normal points are not
- all-equal series produces zero anomalies (no division-by-zero crash)
- window smaller than minimum (`< 3`) returns diagnostic
- explanation contains observed value, mean, stddev, z

**`forecast`**
- monotonically decreasing stock-out crosses 0 at predicted day ±tolerance
- flat series → never crosses → `days_until = +inf` sentinel
- noisy series — smoothing tracks trend, not noise
- explanation contains `alpha`, `trend`, `current`, `crossing_day`

**`predict`**
- pure-noise training data → low confidence, no spurious flags
- training data with one clean rule → tree picks that rule as root
- prediction missing a feature value → returns confidence-discounted result, explanation says which feature was imputed
- explanation `Rules` slice is actual root-to-leaf path

**`classify`**
- k=3, 3 of 3 neighbours same class → confidence 1.0
- k=3, 2 of 3 → confidence 0.67
- k=3 with tie → deterministic tiebreak (lowest-class-id), documented
- explanation lists all k neighbours with similarities

**`cluster by`**
- 3 well-separated blobs → 3 clusters, no noise points
- eps too small → all points are noise
- explanation per row: which cluster, distance to centroid

**`find similar`**
- query record vs 100 corpus, top-1 has higher sim than top-2
- empty corpus → empty result (no panic)
- explanation lists features that drove similarity (top-3 contributing dims)

### 3e. Recommended ordering

1. `learned_threshold` — simplest, validates the Explanation plumbing
2. `is anomaly` — same shape, builds on threshold helpers
3. `forecast` — independent, no model state
4. `predict` — first stateful primitive, exercises the `trained_on` flow
5. `classify` — reuses feature-vector code from `predict`
6. `cluster by` — DBSCAN, reuses distance code from `classify`
7. `find similar` — cosine on the same vectors, mostly glue

Land each behind a feature gate or commit-by-primitive — they're independent.

---

## 4. Milestones and Gating

| Milestone | Exit criteria | Blocks |
|---|---|---|
| **M0 — Dogfood** | `talon run` end-to-end against live Datalevin, 2 overdue vehicles, README brew note fixed | (none, do tomorrow) |
| **M1 — Design doc merged** | ADR-0001 reviewed, decision recorded, `Explanation` interface frozen | M2 |
| **M2 — Planner: `MLComputation` step** | New step type emits for all 7 ML blocks, `planner_test.go` covers each | M3 |
| **M3 — `learned_threshold` + `is anomaly`** | Both keywords compute real values + explanations; `.talon.test` cases pass | M4 |
| **M4 — `forecast`** | Stock-out example produces a real `days_until`, CI assertion strengthened | M5 |
| **M5 — `predict`** | Failure-risk example produces a real probability + decision path | M6 |
| **M6 — `classify` + `find similar`** | Text classification example (e.g. ticket categorisation) ships | M7 |
| **M7 — `cluster by`** | All 7 primitives green, mlruntime registry complete | (closes #11) |

Gating discipline: each milestone **not done** until (a) tests green, (b) one example in `examples/` exercises it, (c) `talon trace` shows the explanation. Without (c) the explainability claim is vapour.

---

## 5. Risks and Trade-offs

### 5a. Does explainability force algorithm choices that cap accuracy? — Yes.

| Primitive | Built-in ceiling | What we lose |
|---|---|---|
| `predict` | CART single tree, depth ~6 | No gradient boosting, no random forest. Accuracy on tabular data typically ~10-15 pp below XGBoost. |
| `classify` | k-NN on TF-IDF | No semantic similarity. "engine fault" and "motor failure" look unrelated. |
| `find similar` | Cosine on hand-engineered features | Same as classify. Hard ceiling without embeddings. |
| `forecast` | Single/double exp smoothing | No seasonality, no exogenous regressors. Fine for monotonic stock-out, weak for weekly demand. |

Pitch defence: Talon doesn't compete with sklearn. Competes with "the engineer hand-writing a threshold check in TypeScript." Against that baseline, CART + clear reasons + audit trail wins.

### 5b. When does the ONNX/sidecar route become unavoidable?

Three specific triggers, in likely order:

1. **`find similar` on free text** — TF-IDF dies on synonym-heavy domains (maintenance tickets, medical notes). First customer with a real text corpus exposes this.
2. **`classify` with > ~5 classes and noisy features** — k-NN's curse of dimensionality. Around the same time as (1).
3. **`predict` where domain experts say "my gut beats the tree"** — happens when there are >20 features with subtle interactions. Less urgent because v1 still ships usable.

**Escape hatch design (cheap to add now):**

```go
// internal/mlruntime/registry.go
type Backend interface{ Compute(...) ... }
var registry = map[string]Backend{
    "predict_decision_tree":  &PredictTreeBackend{},
    "predict_onnx":           nil, // future
    "similarity_cosine":      &CosineBackend{},
    "similarity_embeddings":  nil, // future
}
```

Talon source remains `predict "X" { ... }`; planner picks backend based on tenant config. No language change. Half a day to wire.

### 5c. Other named risks

- **Model persistence undefined.** `trained_on` implies a fit step. Where the trained tree lives is open — `FactStore` blob? Side file? Single biggest unanswered question in design doc.
- **`testrunner` only evaluates first `DatalevinQuery`** (`testrunner.go:62`). Does not run `GoComputation` steps. Without extending it, `.talon.test` files cannot assert on predictions — lose TDD for ML. Plan: extend `testrunner.runOne` to walk full step list using same `mlruntime.Registry`. ~150 LOC.
- **Determinism.** k-NN with ties, DBSCAN with equal-distance neighbours, decision tree feature-split ties — all need documented tiebreak rules. "Deterministic" is in pitch (README.md:21); easy to break in ML code without discipline.
- **Datalevin schema drift on re-seed.** `/schema` closes and reopens the DB (`server.clj:52-54`). If types change between runs, on-disk DB silently misbehaves. Document `rm -rf /tmp/talon-datalevin` in dogfood loop.
- **README ↔ reality drift.** Two examples found: `brew install datalevin` line and unimplemented `talon trace` / `talon repl` (`main.go:36-43`). Worth one cleanup PR before #11 ships.

---

## Critical Files

- `/Users/zh/dev/projects/opentalon-ai/talon-language/internal/mlruntime/mlruntime.go`
- `/Users/zh/dev/projects/opentalon-ai/talon-language/internal/planner/planner.go`
- `/Users/zh/dev/projects/opentalon-ai/talon-language/internal/executor/executor.go`
- `/Users/zh/dev/projects/opentalon-ai/talon-language/internal/testrunner/testrunner.go`
- `/Users/zh/dev/projects/opentalon-ai/talon-language/internal/ast/ast.go`

## Entry Point After `/clear`

Tell next session: "read `docs/design/IMPLEMENTATION_PLAN.md` and start M0 dogfood" (or whichever milestone).

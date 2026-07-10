# ADR-0007: `predict_decision_tree` Primitive (CART)

## Status

Proposed — implements [opentalon/talon-language#71](https://github.com/opentalon/talon-language/issues/71), gates parent [#11](https://github.com/opentalon/talon-language/issues/11). Builds directly on [ADR-0006](0006-classify-knn.md) (supervised-block plumbing).

## Context

`predict` was in the exact pre-classify state: it parsed, planned, and routed
to an `MLComputation{FuncPredictDecisionTree}` step, but the runtime was a
no-op, and — as with the classify issue — the issue's framing was **stale**:

- `predict` had **no target-column concept** (no way to say which attribute is
  the label the tree learns), and
- `planPredictBlock` passed `trained_on` as a raw `*ast.TrainedOnClause` in
  Params rather than materialising a training set.

Everything the supervised plumbing needs already landed with classify_knn
(ADR-0006): `mlruntime.Input.Training`, the dependent `Auxiliary` training
FactQuery, training materialisation in the testrunner, the string-valued
confidence filter, and `{class}` surfacing. This ADR reuses all of it and adds
the CART primitive.

## Decision

**`predict` becomes a supervised block with the same shape as `classify`,
differing only in the model.** It reuses `label_attr` for the target column —
a supervised learner needs a named target, and uniformity with classify beats
inventing a second convention. (The issue's syntax sketch omitted a target
column; this is the same correction classify needed.)

```talon
predict "Failure risk" {
  for records where type == "machine" and status == "in_service"
  features [attr "operating_hours", attr "repair_count"]
  trained_on records where type == "machine" and status == "retired"
  label_attr "outcome"
  confidence >= 0.9
  label "predicted outcome: {class}"
}
```

### The primitive — `internal/mlruntime/predict.go`

CART classification tree, ~250 LoC:

```
Build(samples, depth):
  class, purity = majority(samples)
  if purity == 1 or depth >= max_depth or len < 2*min_samples_leaf: leaf
  split = argmax over (feature, threshold) of Gini decrease,
          subject to min_samples_leaf on both sides
  if no split: leaf
  recurse Build(left, depth+1), Build(right, depth+1)

Walk(candidate):
  follow feature ≤ threshold branches root→leaf
  return leaf.class, leaf.purity, decision path
```

- **Gini decrease is always ≥ 0**, so the split gate is "best valid split"
  (not "strictly positive decrease"). This is what lets the tree solve
  XOR: the first split looks useless (decrease 0), but a second split under it
  separates the classes. `max_depth` / `min_samples_leaf` / purity are the
  stop conditions — verified by the golden XOR fixture.
- **Deterministic**: thresholds are midpoints between distinct sorted values;
  majority ties break lexically (shared `majorityVote` with kNN).
- **No normalisation** (unlike kNN) — axis-aligned threshold splits are
  scale-invariant per feature.
- Stop defaults: `max_depth = 5`, `min_samples_leaf = 5` (issue #71).
- Registered as `NewDecisionTreePredictor()` in `NewRegistry()`.

### Explainability

The decision path is the point of a tree. Each split taken is recorded in the
`Explanation` as a structured `Rule{Attr, Op, Value, Observed}` plus a raw
string list. The testrunner threads those `Rules` into the Decision's **WHY**
lines (`ruleWhyLines`), so `talon explain` prints:

```
WHY
  • operating_hours > 1700 (observed 3100)
```

This is the generic path — any primitive that populates `Explanation.Rules`
now surfaces in explain output, not just the tree.

### Runtime reuse

The only runtime change beyond registration: `narrowByML`'s confidence filter
was generalised from "`s.Function == classify_knn`" to "**string-valued result
+ confidence bound**", so both supervised primitives filter uniformly on leaf
purity / vote fraction. The nested `predict` clause inside a detect block
(which passes no `feature_names`/training) errors inside the primitive and is
swallowed by `narrowByML` → unchanged candidate set, i.e. it stays the inert
no-op it was.

## Consequences

**Bought**

- A second interpretable supervised model, sharing 100% of the classify
  plumbing — the ADR-0006 investment pays off with a ~250-LoC primitive + a
  one-line filter generalisation.
- Decision paths render in `talon explain` for *any* primitive with `Rules`.

**Paid**

- `predict` gains required `trained_on` + `label_attr` clauses (legacy inert
  predict blocks must now name their target). Mechanical; validator + tests
  updated.
- Inherits the executor limitation (below).

## Out of scope (follow-ups)

- **Executor (`talon run`) training materialisation** — shared gap #4 with
  classify / cosine / DBSCAN (ADR-0006). `predict` is delivered + verified via
  the testrunner.
- Configurable `max_depth` / `min_samples_leaf` syntax, cost-complexity
  pruning, regression trees (continuous target), and probability-vector output.
- Model persistence / nightly retrain (RFC sketch) — train-on-demand for v1.

# Prediction (`predict` + decision tree)

A `predict` block assigns each matched record a class using a **CART decision
tree** trained on past, already-labeled records. Where [`classify`](classify.md)
votes among nearest neighbours, `predict` learns explicit **if/then splits** —
"operating_hours > 3000 AND repair_count > 3 → failed" — and the splits it took
*are* the explanation. That interpretability is why the RFC picked trees over
opaque models: a reviewer can read the reasoning, and `tln explain` prints it.

No training run, no model file: the labeled examples *are* the model, read from
the FactStore and the tree grown on demand.

## Syntax

```tln
predict "Failure risk" {
  for records where type == "machine" and status == "in_service"
  features [attr "operating_hours", attr "repair_count"]
  trained_on records where type == "machine" and status == "retired"
  label_attr "outcome"
  confidence >= 0.9
  label "predicted outcome: {class}"
  priority HIGH
}
```

| Clause | Meaning |
|---|---|
| `for records where …` | the **candidates** to score |
| `features [ … ]` | the numeric attributes the tree splits on |
| `trained_on records where …` | the **labeled examples** the tree learns from |
| `label_attr "…"` | which attribute on those examples is the target class |
| `confidence >= N` | *(optional)* drop predictions from an impure leaf (purity < N) |
| `label "… {class} …"` | `{class}` interpolates the predicted class |

`features`, `trained_on`, and `label_attr` are required. `predict` shares the
`label_attr` target-column convention with `classify` — a supervised learner
needs a named target.

## How the tree works

**Training** (CART, Gini impurity):

1. If the training rows at a node all share a label, or the depth cap
   (default 5) is hit, or the node is too small to split — make a **leaf** with
   the majority label.
2. Otherwise, try every feature and every candidate threshold (midpoints
   between observed values) and pick the split that most **reduces Gini
   impurity**, subject to `min_samples_leaf` (default 5) rows on each side.
3. Recurse on the left (`≤ threshold`) and right (`> threshold`) subsets.

`min_samples_leaf` is the overfitting guard: a lone mislabeled outlier can't
carve out its own leaf, so it gets absorbed into the surrounding majority.

**Prediction:** walk each candidate root→leaf by its feature values. The
predicted class is the leaf's majority label; **confidence** is the leaf's
purity (fraction sharing that label). With `confidence >= N`, a candidate that
lands on an impure leaf (purity below `N`) is left **unflagged**.

## Worked example

Files: [`examples/failure_risk.tln`](../examples/failure_risk.tln) and
[`test/failure_risk.tln.test`](../test/failure_risk.tln.test).

Ten retired machines are the training set, with a clean signal:

| outcome | operating_hours | repair_count |
|---|---|---|
| `failed` (×5) | ~3000 | 4–7 |
| `ok` (×5) | ~500 | 0–1 |

Two in-service machines are scored:

| id | operating_hours | repair_count | prediction |
|---|---|---|---|
| 100 | 3100 | 6 | **failed** (pure leaf, confidence 1.0) |
| 101 | 450 | 0 | **ok** (pure leaf, confidence 1.0) |

Run it:

```
./tln build   examples/failure_risk.tln
./tln test    examples/failure_risk.tln test/failure_risk.tln.test
./tln explain examples/failure_risk.tln test/failure_risk.tln.test
```

`tln explain` shows the predicted class **and the split that produced it**:

```
ACTION    predicted outcome: failed
ITEM      entity #100
WHY
  • operating_hours > 1700 (observed 3100)
EVIDENCE
  operating_hours = 3100
  repair_count = 6
```

The tree learned a single split at `operating_hours ≈ 1700` cleanly separates
the two outcomes in this fixture; machine #100 took the `>` branch. A messier
dataset produces a deeper path (`operating_hours > … AND repair_count > …`),
each condition a bullet under WHY.

## `predict` vs `classify`

Both are supervised: same `features` / `trained_on` / `label_attr` / `{class}`
shape. They differ in the model:

- **`classify` (kNN)** — no explicit model; a row is labeled by its nearest
  neighbours. Good when classes form blobs in feature space; the "why" is the
  neighbours that voted.
- **`predict` (decision tree)** — learns axis-aligned rules; the "why" is a
  readable if/then path. Good when the signal is threshold-shaped
  ("high hours *and* many repairs") and you want the rule written out.

## Limitations

- **`tln test` / `tln explain` are fully supported.** The `tln run`
  executor path doesn't yet materialise the in-memory training set for
  supervised primitives (shared with `classify` / `find similar` / `cluster`);
  `predict` degrades to "no predictions" there rather than erroring. Tracked in
  [ADR-0006](design/0006-classify-knn.md) / [ADR-0007](design/0007-predict-decision-tree.md).
- `max_depth` (5) and `min_samples_leaf` (5) are fixed defaults; configurable
  syntax, pruning, and regression trees are follow-ups in ADR-0007.
- Non-numeric features contribute 0 to the split space — the tree learns on
  numeric signal. Model persistence (train-nightly / serialize) is deferred.

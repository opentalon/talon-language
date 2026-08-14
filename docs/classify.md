# Classification (`classify` + kNN)

A `classify` block assigns each matched record a **class**, learned from
past records you've already labeled. It answers "which of these known
categories does this new thing look most like?" — routing an incident to a
failure mode, a ticket to a queue, a transaction to a risk band.

The engine is k-nearest-neighbours (kNN): to classify a record, find the *k*
labeled examples whose features are closest to it and take the majority vote.
No training run, no model file — the labeled examples *are* the model, read
fresh from the FactStore each time. That keeps the decision fully auditable:
`tln explain` can name the exact neighbours that voted.

## Syntax

```tln
classify "Failure mode" {
  for records where type == "incident" and status == "open"
  features [attr "vibration", attr "temp"]
  trained_on records where type == "incident" and status == "resolved"
  label_attr "root_cause"
  confidence >= 0.8
  label "likely cause: {class}"
  priority HIGH
}
```

| Clause | Meaning |
|---|---|
| `for records where …` | the **candidates** to classify |
| `features [ … ]` | the numeric attributes distance is measured on |
| `trained_on records where …` | the **labeled examples** the vote draws from |
| `label_attr "…"` | which attribute on those examples holds the class |
| `confidence >= N` | *(optional)* drop predictions whose winning vote fraction is below `N` |
| `label "… {class} …"` | `{class}` interpolates the predicted class into the output |

`features`, `trained_on`, and `label_attr` are required — a classifier with
no labeled examples or no label column has nothing to learn from, and the
validator says so.

## How the vote works

1. Build a feature vector for every candidate and every training row from the
   named `features`.
2. **Z-normalise each feature column** across candidates ∪ training, so an
   attribute measured in the thousands (`temp`) doesn't drown out one measured
   in single digits — every axis contributes on equal footing.
3. For each candidate, compute the **euclidean distance** to every training
   row and keep the `k` nearest (default `k = 5`).
4. **Majority-vote** their labels. The winner is the predicted class;
   **confidence** is its share of the `k` votes (e.g. 4 of 5 → `0.8`). Ties
   break to the lexically smaller label, deterministically.

If you set `confidence >= N`, a candidate whose winning vote is weaker than
`N` is left **unflagged** — a split vote between two classes is exactly the
"I'm not sure" signal you want to withhold. Without the bound, `classify`
labels every candidate and flags them all (it's informational, not a filter).

## Worked example

Files: [`examples/incident_triage.tln`](../examples/incident_triage.tln)
and [`test/incident_triage.tln.test`](../test/incident_triage.tln.test).

The training set is ten resolved incidents with two clear signatures:

| root_cause | vibration | temp |
|---|---|---|
| `bearing` (×5) | ~90 | ~20 |
| `overheat` (×5) | ~20 | ~90 |

Three open incidents arrive to be classified:

| id | vibration | temp | outcome |
|---|---|---|---|
| 100 | 91 | 21 | all 5 neighbours are `bearing` → **bearing**, confidence 1.0 |
| 101 | 20 | 90 | all 5 neighbours are `overheat` → **overheat**, confidence 1.0 |
| 102 | 55 | 55 | midway — vote splits, confidence < 0.8 → **dropped** |

Run it:

```
./tln build   examples/incident_triage.tln
./tln test    examples/incident_triage.tln test/incident_triage.tln.test
./tln explain examples/incident_triage.tln test/incident_triage.tln.test
```

`tln test` confirms 100 and 101 are flagged and 102 is not. `tln explain`
shows the rendered class per incident:

```
== route open incidents to a failure mode ==
ACTION    likely cause: bearing
ITEM      entity #100
ACTION    likely cause: overheat
ITEM      entity #101
```

Incident 102 produces no decision — its vote never cleared the `0.8` bar.

## Explainability

Every prediction carries its neighbours in the explanation: the `k` training
rows that voted, each with its id, label, and distance. That's the audit
trail — "incident 100 was called `bearing` because its five nearest resolved
incidents (#1, #4, #2, #5, #3) were all bearing failures" — not a black-box
score. This is the same first-class-explanation contract every tln ML
primitive follows (see [ADR-0001](design/0001-ml-runtime-strategy.md)).

## Limitations

- **`tln test` / `tln explain` are fully supported.** The `tln run`
  executor path doesn't yet materialise the in-memory training set for
  multi-attribute primitives (the same pending work `find similar` and
  `cluster` need); classify degrades to "no predictions" there rather than
  erroring. Tracked in [ADR-0006](design/0006-classify-knn.md).
- `k` is fixed at 5. Distance-weighted voting, alternative metrics, and a
  configurable `k` are noted as follow-ups in ADR-0006.
- Non-numeric features contribute 0 to the vector — kNN here classifies on
  numeric signal.

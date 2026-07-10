// kNN classification: route a new incident to a failure mode by looking at
// how past, already-resolved incidents with similar sensor readings were
// labeled. This is the `classify` block made real by the classify_knn
// primitive (issue #70, ADR-0006).
//
// How it reads:
//   - `for records where ...`  — the candidates to classify (open incidents).
//   - `features [...]`         — the numeric axes the distance is measured on.
//   - `trained_on records ...` — the labeled examples the vote is drawn from.
//   - `label_attr "..."`       — which attribute on those rows holds the class.
//   - `confidence >= N`        — drop predictions whose winning vote fraction
//                                falls below N (an uncertain, split vote).
//   - `{class}` in the label   — the predicted class for the row.
//
// Each candidate is assigned the majority class of its 5 nearest neighbours
// (euclidean distance over per-feature z-normalised vectors), with
// confidence = the winning class's share of those 5 votes. The neighbours
// that voted ride along in the explanation, so `talon explain` shows exactly
// why an incident was routed the way it was.
//
//   ./talon build   examples/incident_triage.talon
//   ./talon test    examples/incident_triage.talon test/incident_triage.talon.test
//   ./talon explain examples/incident_triage.talon test/incident_triage.talon.test
//
// Note: classify runs end-to-end under `talon test` / `talon explain` (the
// testrunner materialises the training set in memory). The `talon run`
// executor path shares the pending training-materialisation work tracked for
// the other multi-attribute primitives (cosine / DBSCAN) — see ADR-0006.

classify "Failure mode" {
  for records where type == "incident" and status == "open"
  features [attr "vibration", attr "temp"]
  trained_on records where type == "incident" and status == "resolved"
  label_attr "root_cause"
  confidence >= 0.8
  label "likely cause: {class}"
  priority HIGH
}

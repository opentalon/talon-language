// Predict vehicle failure risk from an imported decision-tree model.
//
// `using model "risk.tree.failure_risk"` walks the model's inline fitted tree
// instead of training on a `trained_on` query — the eager-model counterpart to
// the kNN `using model` in fleet_risk.talon.
//
// Run:
//   go build ./cmd/talon
//   ./talon test examples/fleet_risk_tree.talon test/fleet_risk_tree.talon.test

import "risk.tree"

predict "Vehicle failure risk (tree)" {
  for records where type == "vehicle" and status == "open"
  using model "risk.tree.failure_risk"
  confidence >= 0.9
  label "risk: {class}"
  priority HIGH
}

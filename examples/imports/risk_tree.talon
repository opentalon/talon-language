// risk.tree — a decision-tree ML model with inline fitted params.
//
// Unlike a kNN model (lazy — the fitted params ARE the labeled examples), a
// decision tree is eager: its fitted params are the tree itself. `fitted tree`
// stores the splits + leaves as flat, index-referenced nodes (root = node 0;
// `feature <= threshold` goes left). The predictor walks this tree directly —
// no training, fully deterministic and version-pinned in source.
//
//   node 0: km <= 30000 ?  left → node 1 (low)  right → node 2
//   node 2: age <= 5    ?  left → node 3 (low)  right → node 4 (high)

module "risk.tree" {
  export model "failure_risk" {
    predict tree
    features [attr "km", attr "age"]
    fitted tree {
      node 0 split "km" 30000 left 1 right 2
      node 1 leaf "low" 1.0
      node 2 split "age" 5 left 3 right 4
      node 3 leaf "low" 0.8
      node 4 leaf "high" 1.0
    }
    computed_from "CART on 1204 vehicles, depth 2"
    valid_until "2026-12-31"
  }
}

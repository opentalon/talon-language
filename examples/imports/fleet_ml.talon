// fleet.ml — an importable ML package (issue #13 ML-module system).
//
// A `module` namespaces its exported members; a `model` block carries inline
// `fitted` params. For the lazy kNN classifier the fitted params ARE the
// labeled examples, so the model is fully self-contained and version-pinned
// in source (computed_from / valid_until) — no host training job.
//
// Referenced from another file as `fleet.ml.failure_risk` after `import`.

module "fleet.ml" {
  export model "failure_risk" {
    classify knn k 3
    features [attr "km", attr "age"]
    fitted {
      example [50000, 8] label "high"
      example [52000, 9] label "high"
      example [48000, 7] label "high"
      example [10000, 2] label "low"
      example [12000, 3] label "low"
      example [8000, 1]  label "low"
    }
    computed_from "1204 labeled vehicles, 2026-Q2"
    valid_until "2026-12-31"
  }
}

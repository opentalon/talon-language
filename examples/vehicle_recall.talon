// Derived predicates: `derive` names a boolean predicate over a record, and
// any other block references it as `pred(v)` — the deduction that used to live
// in host glue now lives in the language. A predicate reads exactly like an
// asserted fact; the planner inlines its body into the referencing query
// (issue #91, docs/derive.md).
//
// Here a two-step deductive chain runs end-to-end with no host code:
//   derive overdue(v)        — a vehicle past its service interval
//   detect "Recall candidates" — overdue AND a recalled model
//   recommend "Book recall"    — fires off the detect
//
//   ./talon build   examples/vehicle_recall.talon
//   ./talon test    examples/vehicle_recall.talon test/vehicle_recall.talon.test
//   ./talon explain examples/vehicle_recall.talon test/vehicle_recall.talon.test
//
// v1 is arity-1 and non-recursive (the validator rejects cycles). Recursive /
// arity-N predicates via the FactStore's recursive rule resolver are a tracked
// follow-up — the machinery already exists (see `category_tree`).

derive overdue(v) {
  for records where type == "vehicle"
    and attr "km" > attr "last_service_km" + 20000
}

detect "Recall candidates" {
  for records where overdue(v)
    and attr "model" in ["Transit", "Sprinter"]
  flag matching items
  label "{item.name}: recall candidate ({attr.km} km, model {attr.model})"
  priority HIGH
}

recommend "Book recall service" {
  when detect "Recall candidates" matches
  suggest "book {item.name} in for the recall service"
  priority HIGH
}

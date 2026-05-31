// Same shape as parts_reorder.talon but with `solver linear` — route to
// the exact ILP solver. Returns a SINGLE proven-optimal subset rather
// than a Pareto frontier of approximations.
//
// Use this when:
//   - The objective and constraints are linear sums of attrs (no avg(), no products)
//   - You want a provably optimal answer for audit / regulatory reasons
//   - You don't need the GA's "robustness across the frontier" narrative
//
// For multi-objective or nonlinear cases, drop `solver linear` to use GA.
//
//   ./talon build examples/parts_reorder_exact.talon
//   ./talon test examples/parts_reorder_exact.talon test/parts_reorder_exact.talon.test
//   ./talon explain examples/parts_reorder_exact.talon test/parts_reorder_exact.talon.test

combine "Reorder exact" {
  for records where type == "stock_item" and status == "active"
  solver linear
  select 3 from records
  maximize total(attr "downstream_blast_radius")
  subject_to total(attr "reorder_cost") <= 5000
  return id, reorder_cost, downstream_blast_radius
  label "Reorder {item.name}: ${attr.reorder_cost}, blocks {attr.downstream_blast_radius} jobs"
  priority HIGH
}

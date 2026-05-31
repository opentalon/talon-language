// Multi-objective dispatch: pick vehicles that are simultaneously cheap to
// operate AND high-urgency. With two competing objectives there's no single
// "best" — combine returns the Pareto frontier (rank 0). Tier-1 explainability
// cites which dimensions each pick wins on and how many others it dominates.
//
//   go build ./cmd/talon
//   ./talon build examples/fleet_dispatch.talon
//   ./talon test examples/fleet_dispatch.talon test/fleet_dispatch.talon.test
//   ./talon explain examples/fleet_dispatch.talon test/fleet_dispatch.talon.test

define "dispatchable" {
  type == "item"
  and status == "active"
  and category == "Vehicles"
}

combine "Dispatch picks" {
  for records where is "dispatchable"
  minimize attr "cost_per_km"
  maximize attr "urgency_score"
  return id, cost_per_km, urgency_score
  label "Dispatch {item.name}: cost {attr.cost_per_km}, urgency {attr.urgency_score}"
  priority HIGH
}

// `calculate` reduces a series of records to a single scalar bound to a
// variable name, then `having` filters candidates on it (a post-aggregation
// filter) and labels interpolate it with `{var}`.
//
// Methods: `average` (the default once a value column is named), `sum`,
// `count`, and `weighted_moving_average` — the recency-weighted "current
// rate" case where recent observations weigh more (see grammar.ebnf).
//
// Run the companion suite:
//   go build ./cmd/talon
//   ./talon test examples/consumption_rate.talon examples/consumption_rate.talon.test
detect "High average consumption" {
  for records where type == "depot"
  calculate avg_use from records where type == "usage" of attr "amount" average
  having avg_use > 100
  flag matching items
  label "fleet averaging {avg_use} units/day — review restock policy"
  priority HIGH
}

// weighted_moving_average form — recent days weigh more, so a slowdown last
// week shifts the rate more than usage from 30 days ago. The primitive is
// exercised in internal/mlruntime; wiring its per-record series from the
// FactStore's time dimension is a shared follow-up with `forecast`.
detect "Recent consumption rate" {
  for records where type == "depot"
  calculate daily_rate from activities
    where type == "usage"
    of attr "amount"
    weighted_moving_average last 30 days
  flag matching items
  label "recent daily rate ~{daily_rate}"
  priority MEDIUM
}

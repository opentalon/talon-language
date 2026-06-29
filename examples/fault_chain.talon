// Fault chain detection — illustrates the `record A followed_by record B`
// sequence syntax (issue #61). Vehicles that experienced an electrical
// fault and then an engine failure within a 30-day window get flagged
// for inspection.

detect "Engine failure chain" {
  for records where type == "vehicle"
    and record type "electrical_fault"
        followed_by record type "engine_failure"
        on same item within 30 days
  flag matching items
  label "{item.name}: electrical fault preceded engine failure"
  priority HIGH
}

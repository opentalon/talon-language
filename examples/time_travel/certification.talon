// Time-travel detection — "certification regressed".
//
// `was ( <condition> ) N <unit> ago` evaluates the inner condition against
// the fact store's state N units in the PAST, then intersects with the
// present-day candidates. It runs on any FactStore that implements the
// TimeTraveler capability — here, talon-db (per-document version history).
//
// This flags machines that are DEFECTIVE now but were CERTIFIED 90 days
// ago: a genuine regression, distinct from machines that were already
// defective back then (never certified) or that are still certified today.
detect "Certification regressed" {
  for records where type == "machine"
    and status == "defective"
    and was (status == "certified") 30 days ago
  flag matching items
  label "{item.name}: certified 90d ago, defective now — investigate regression"
  priority HIGH
}

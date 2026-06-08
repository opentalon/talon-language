// Example: defeasible rules, reactive on-blocks, and integrity constraints.
// See docs/defeasible.md, docs/reactive.md, docs/constraints.md.

// ─── Defeasible rules ─────────────────────────────────────────────────────────

// A strict rule cannot be overridden by anything.
strict rule "Expired certification blocks assignment" {
  for records where type == "person"
  before "assign"
  block "assign"
  reason "Safety certification expired — non-negotiable"
}

// Default-deny: blocks all deletions unless a more specific rule says otherwise.
rule "Block all deletions" {
  when tool_action contains "delete"
  block "delete"
  reason "Deletion not allowed by default"
  priority LOW
}

// More specific rule defeats the default. Two rules at different priorities
// with an explicit override edge resolve cleanly.
rule "Cleanup crew can delete" {
  when tool_action contains "delete"
  overrides "Block all deletions"
  allow "delete"
  priority HIGH
}

// Even more specific — overrides the override during audit windows.
rule "No deletions on audit days" {
  when tool_action contains "delete"
  overrides "Cleanup crew can delete"
  block "delete"
  reason "No deletions during audit period"
  priority CRITICAL
}

// ─── Reactive rules ───────────────────────────────────────────────────────────

// Fire immediately when stock crosses below the minimum.
on change attr "current_stock" {
  logger.warn "stock changed for {item.name}"
  recommend "Order stock"
}

// Fire when a new activity is asserted, so safety detections do not have to
// wait for the scheduled scan.
on assert activity {
  detect "Defective item without ticket"
}

// Fire when an item is removed, to flag orphaned activities.
on retract item {
  logger.warn "item removed: {item.id}"
}

// ─── Integrity constraints ────────────────────────────────────────────────────

// Reject items whose status came from a buggy MCP tool with a typo.
constraint "Item status is valid" {
  for records where type == "item"
  require attr "status" in ["active", "defective", "missing", "inactive"]
  on_violation reject "invalid item status — likely a typo from upstream"
}

// Stock cannot be negative; reject the assert so the FactStore stays clean.
constraint "Stock cannot be negative" {
  for records where type == "stock_item"
  require attr "current_stock" >= 0
  on_violation reject "stock cannot be negative"
}

// Quarantine suspicious dates instead of dropping them — admins can review.
constraint "Activity date not in the future" {
  for records where type == "activity"
  require attr "activity_date" <= 0
  on_violation quarantine "activity_date is in the future"
}

// A recommend block is referenced by the reactive on-block above. Kept
// minimal so the example parses and validates cleanly.
recommend "Order stock" {
  when attr "current_stock" <= 0
  suggest "Order more stock for {item.name}"
  priority HIGH
}

// A detect block likewise referenced by the on-block above.
detect "Defective item without ticket" {
  for records where type == "item"
    and status == "defective"
  flag matching items
  label "{item.name} is defective without an open ticket"
  priority CRITICAL
}

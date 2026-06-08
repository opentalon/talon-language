// Insurance-claims auto-adjudication, modelled after the worked example in
// https://opakalex.github.io/posts/code-mode-for-mcp/.
//
// The LLM extracts facts from each claim invoice (PDF → record + attrs).
// Talon then decides — deterministically — whether the claim is auto-approved,
// auto-rejected, or escalated to a human reviewer. No LLM in the decision
// loop; every outcome is traceable to a specific rule.
//
//   ./talon build examples/insurance_claims.talon
//   ./talon test examples/insurance_claims.talon test/insurance_claims.talon.test
//   ./talon explain examples/insurance_claims.talon test/insurance_claims.talon.test
//
// Or in the REPL:
//   ./talon repl
//   talon> :load examples/insurance_claims.talon
//   talon> :load test/insurance_claims.talon.test
//   talon> :eval all
//   talon> :trace "Auto-approve in-network routine"

// ─── Policy rules ─────────────────────────────────────────────────────────────

// Any claim from a blacklisted provider must be blocked. CRITICAL priority so
// it wins against any downstream "auto-approve" rule that also matches.
rule "Reject blacklisted provider" {
  for records where type == "claim"
    and attr "provider_status" == "blacklisted"
  before "approve_claim"
  block "approve_claim"
  reason "Provider {attr.provider_id} is on the fraud blacklist"
  priority CRITICAL
}

// Routine outpatient claims from in-network providers within the per-visit
// cap are safe to auto-approve.
rule "Auto-approve in-network routine" {
  for records where type == "claim"
    and attr "provider_status" == "in_network"
    and attr "service_category" == "outpatient"
    and attr "amount_chf" <= attr "per_visit_cap"
  before "approve_claim"
  allow "approve_claim"
  priority HIGH
}

// ─── Surfacing — what needs a human ──────────────────────────────────────────

// Claims that exceed the per-visit cap need a human. Don't auto-decide.
detect "Over the per-visit cap" {
  for records where type == "claim"
    and attr "amount_chf" > attr "per_visit_cap"
  flag matching items
  label "Claim {item.id}: {attr.amount_chf} CHF over the per-visit cap ({attr.per_visit_cap})"
  priority HIGH
}

// Claims from out-of-network providers need a human regardless of amount.
detect "Out-of-network provider" {
  for records where type == "claim"
    and attr "provider_status" == "out_of_network"
  flag matching items
  label "Claim {item.id}: out-of-network provider {attr.provider_id} — needs pre-authorization"
  priority HIGH
}

// ─── Operational follow-up ───────────────────────────────────────────────────

// For every claim a reviewer touches, suggest the next step. The recommend
// block ties to the detect block above so the chain is explainable end-to-end
// via `talon explain`.
recommend "Schedule reviewer" {
  when detect "Over the per-visit cap" matches
  suggest "Route claim {item.id} ({attr.amount_chf} CHF) to senior adjuster — exceeds cap by {attr.amount_chf - attr.per_visit_cap} CHF"
  priority HIGH
}

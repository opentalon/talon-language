// Package talondb is the custom Go-native embedded fact database for Talon.
// Implementation begins in Phase 3a after Datalevin validates access patterns.
// See issues #25–#31 for the full spec.
//
// Pinned for Phase 3a: a RETE-based incremental match engine — issue #89.
// RETE compiles a block's selector into a discrimination network at plan
// time and maintains match state across mutations, so reactive `on assert |
// retract | change` blocks consume token deltas instead of rescanning the
// FactStore on every event. RETE's index requirements (alpha-memory lookup
// keyed on attribute, beta-memory hash joins) shape the storage design,
// which is why it's filed against this package rather than retrofitted
// later. See #89 for the engine surface, scope, and prerequisites
// (EventAssert / EventChange emission).
package talondb

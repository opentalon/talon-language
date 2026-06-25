// Package talondb is the FactStore adapter that talks to a remote
// talondb-server (gRPC over Unix socket or TCP). The package mirrors
// the shape of internal/datalevin: a thin Client with method wrappers
// + an Adapter that implements factstore.FactStore.
//
// Today the adapter supports the subset of Query clauses that the
// fleet_maintenance.talon example exercises (Pattern + Predicate).
// Unsupported clauses (Or, Not, FullText, RuleCall, Aggregate,
// PullSpec) return errors.ErrUnsupported; the planner audit confirms
// fleet_maintenance does not emit them.
package talondb

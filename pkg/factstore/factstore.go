// Package factstore is the public backend SPI for tln. A storage backend
// implements [FactStore] to become usable with [tln.Run] via
// [tln.WithFactStore]; the query AST types below are what a backend's Query
// method receives and inspects (type-switch over [Clause]).
//
// These are type aliases to tln's internal factstore package, so a backend in
// another module (e.g. github.com/opentalon/tln-db) can construct and match on
// exactly the types tln's planner emits — no separate adapter layer, no copied
// structs. Hosts that only *run* programs want [github.com/opentalon/tln-language/pkg/tln];
// this package is for authors of storage backends.
package factstore

import fs "github.com/opentalon/tln-language/internal/factstore"

// Core contract. A backend implements FactStore; the optional interfaces are
// probed with a type assertion by the runtime when present.
type (
	// FactStore is the contract [tln.WithFactStore] accepts.
	FactStore = fs.FactStore
	// TimeTraveler is the optional `was ( … ) N ago` history interface.
	TimeTraveler = fs.TimeTraveler
	// Freshness is the optional staleness-reporting interface.
	Freshness = fs.Freshness
)

// Query AST — what FactStore.Query receives and a backend translates.
type (
	Query     = fs.Query
	Clause    = fs.Clause
	Pattern   = fs.Pattern
	Predicate = fs.Predicate
	Or        = fs.Or
	Not       = fs.Not
	FullText  = fs.FullText
	RuleCall  = fs.RuleCall
	Rule      = fs.Rule
	Aggregate = fs.Aggregate
	PullSpec  = fs.PullSpec
	Term      = fs.Term
	// Negation is negation-as-failure of a rule-call inside a Rule body — what
	// an out-of-tree solver (e.g. the tln-asp ASP plugin) needs to express
	// `not p(x)`. The core resolver reads it under well-founded semantics.
	Negation = fs.Negation
)

// Mutation + event types.
type (
	Fact           = fs.Fact
	RetractPattern = fs.RetractPattern
	Event          = fs.Event
	EventKind      = fs.EventKind
)

// EventKind values, mirrored so backends emitting events name them here.
const (
	EventAssert  = fs.EventAssert
	EventRetract = fs.EventRetract
	EventChange  = fs.EventChange
)

// MemoryStore is the in-process reference backend — useful as a test double
// and the model implementation a new backend can check itself against.
type MemoryStore = fs.MemoryStore

// NewMemoryStore returns an empty in-process store.
func NewMemoryStore() *MemoryStore { return fs.NewMemoryStore() }

// Var builds a variable Term (a leading "?" is added if missing).
func Var(name string) Term { return fs.Var(name) }

// Lit builds a literal Term.
func Lit(v any) Term { return fs.Lit(v) }

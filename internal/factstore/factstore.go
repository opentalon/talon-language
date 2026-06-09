// Package factstore is Talon's database abstraction. The planner emits
// structured Query values that any FactStore implementation can answer,
// so adding a new backend (in-memory, SQL, custom) does not touch the
// planner — only the implementation. See docs/factstore.md and issue
// #14.
//
// The interface deliberately stays small: query + assert. Schema setup,
// recursive traversal, time-travel queries, and other capabilities the
// RFC mentions are not yet wired through any planner path; they will
// land as the planner needs them, alongside the implementation that
// supports them.
package factstore

import "context"

// FactStore is the single interface the executor talks to. Implementations
// shipped in this repo: *datalevin.Client (Datalevin over HTTP) and
// MemoryStore (Prolog-style in-memory store). External callers can plug
// in their own; the contract is small enough to mock.
type FactStore interface {
	// Query evaluates a structured query and returns one row per match,
	// columns ordered by Query.Find.
	Query(ctx context.Context, q Query) ([][]any, error)

	// Assert adds facts to the store. Implementations are responsible for
	// any persistence concerns (Datalevin commits immediately; MemoryStore
	// mutates an in-process map; a future SQL store batches into INSERTs).
	Assert(ctx context.Context, facts []Fact) error
}

// Fact is a single EAV triple. The record-ID + attribute together identify
// the cell being asserted; value carries the data; timestamp is optional
// and zero-valued when the caller does not need temporal semantics.
//
// The historical FactStore interface from the RFC distinguishes Entity
// (tenant) and RecordID (within tenant). Today's executor only deals with
// one tenant at a time, so the planner emits queries without a tenant
// pattern. The Entity field is reserved for the multi-tenant work in
// follow-up issues #15 / #4.
type Fact struct {
	Entity    string // tenant ID (unused today; reserved for #15)
	RecordID  string
	Attribute string
	Value     any
}

// Query is a single read from the FactStore. Datalog-shaped but Go-typed:
// the patterns bind variables across entities, the predicates filter the
// bindings, and Find names the columns to return (in order).
//
// Backends do not need to parse text — every shape that the planner can
// express lives in this struct.
type Query struct {
	// Find names the variables to return, in column order. "?e" is
	// conventionally the entity binding (column 0).
	Find []string

	// Where is the clause list. All clauses must hold for a row to match;
	// nested Or and Not clauses handle disjunction and negation.
	Where []Clause

	// Aggregates rolls matched rows up into summary values. When set,
	// the result has one row per GroupBy combination (or exactly one row
	// when GroupBy is empty), with one column per aggregate plus the
	// group-by columns. Find is ignored when Aggregates is non-empty —
	// the result columns come from GroupBy + Aggregates instead. See
	// docs/factstore.md.
	Aggregates []Aggregate

	// GroupBy names the variables to partition aggregates by. Empty
	// means "one aggregate row for the whole match set". The variables
	// must be bound by the patterns in Where.
	GroupBy []string
}

// Aggregate is a single roll-up of matched rows.
//
// `Fn` is one of "count", "sum", "avg", "min", "max". `Over` is the
// variable being aggregated; for "count" without an argument, set Over
// to Var("?e") (count rows) or a wildcard Term{} (also counts rows).
// `As` is the column name in the result row.
type Aggregate struct {
	Fn   string
	Over Term
	As   string
}

// Clause is implemented by Pattern, Predicate, Or, and Not.
type Clause interface {
	clauseNode()
}

// Pattern matches an entity-attribute-value triple. Each field can be a
// variable (Term.Var set), a literal (Term.Literal set), or a wildcard
// (both empty). A variable that already has a binding constrains the
// match; an unbound variable receives the matched value.
type Pattern struct {
	Entity    Term
	Attribute string // ":record/type", ":attr/km", etc. — namespace included
	Value     Term
}

// Predicate is a post-binding comparison or string match. Backends with
// query languages of their own translate this; MemoryStore evaluates it
// directly.
type Predicate struct {
	Op    string // "<", "<=", ">", ">=", "==", "!=", "starts_with", "ends_with", "contains"
	Left  Term
	Right Term
}

// Or is the disjunction of N branches. A row matches if any branch
// matches; variables bound inside a branch are scoped to that branch.
type Or struct {
	Branches [][]Clause
}

// Not negates the inner clauses: the row matches only when none of them
// match.
type Not struct {
	Body []Clause
}

// FullText matches an entity whose facts contain Query as a full-text
// term. MemoryStore evaluates this as a substring scan across every
// attribute value on the entity. The Datalevin backend renders it to
// the `(fulltext $ "query")` predicate so the server can use its
// native FTS indices when configured.
//
// Entity is the entity variable the match binds (conventionally
// "?e") so the FullText clause plays well with sibling Pattern
// clauses on the same row.
type FullText struct {
	Entity Term
	Query  string
}

func (*Pattern) clauseNode()   {}
func (*Predicate) clauseNode() {}
func (*Or) clauseNode()        {}
func (*Not) clauseNode()       {}
func (*FullText) clauseNode()  {}

// Term carries either a variable reference or a literal value. Use the
// constructors below — the zero value is the wildcard "any".
type Term struct {
	Var     string // non-empty for a variable reference (e.g. "?e", "?km")
	Literal any    // non-nil for a literal — string, float64, bool, int
}

// Var constructs a variable term. The conventional leading "?" is added
// if missing so call-sites can read naturally.
func Var(name string) Term {
	if name == "" {
		return Term{}
	}
	if name[0] != '?' {
		name = "?" + name
	}
	return Term{Var: name}
}

// Lit constructs a literal term.
func Lit(v any) Term { return Term{Literal: v} }

// IsVar reports whether the term references a variable.
func (t Term) IsVar() bool { return t.Var != "" }

// IsWildcard reports whether the term is the zero value (matches any).
func (t Term) IsWildcard() bool { return t.Var == "" && t.Literal == nil }

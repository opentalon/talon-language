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

	// Retract removes matching cells from the store. RecordID is required
	// and identifies the target entity. When Attribute is empty, every
	// cell on the entity is removed (the entity is effectively dropped).
	// When Attribute is set and Value is non-nil, only the cell whose
	// value equals Value is removed; when Value is nil, every cell with
	// that attribute is removed regardless of value.
	//
	// MemoryStore emits a Retract event for each removed cell so the
	// reactive dispatcher can fire registered `on retract` blocks.
	Retract(ctx context.Context, pattern RetractPattern) error
}

// RetractPattern names the cells to remove from a FactStore. RecordID
// is required; Attribute + Value are optional. See FactStore.Retract.
type RetractPattern struct {
	RecordID  string
	Attribute string // "" = retract the whole entity
	Value     any    // nil = retract every value of Attribute
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

	// Rules carries recursive Datalog rule definitions invoked by
	// RuleCall clauses inside Where. Multiple Rules entries sharing
	// the same Name form a disjunction (Datalog semantics): a call
	// matches if any rule with that name binds successfully.
	//
	// Datalevin receives these as a separate `:rules` parameter to
	// d/q; MemoryStore evaluates them via fixed-point semi-naive
	// expansion. Planner emits these when it sees a category_tree(X)
	// expression so the recursion stays at the store level instead
	// of degrading to a Go-side filter.
	Rules []Rule

	// Pull, when non-empty, replaces the :find columns with one
	// `(pull ?e [...])` expression per entry. Each entry is the
	// Datalog pull-pattern body (e.g. `[:* {:friends 2}]`) and is
	// rendered verbatim — callers own the pull-syntax surface.
	Pull []PullSpec
}

// PullSpec is one column-replacement directive: take entity binding
// EntityVar and expand it via the Pattern pull-syntax string. The
// Pattern is a Datalog literal (e.g. `[:record/category :category/name]`
// or `[:* {:category 2}]`) rendered as-is.
type PullSpec struct {
	EntityVar string
	Pattern   string
}

// Rule is one clause of a recursive Datalog rule, in the same shape as
// Datalevin's rule-vector form. `Name` is the predicate name (no
// leading "?"); `Args` are the variable names the head exposes; `Body`
// is the conjunction of clauses that must hold to bind the head.
type Rule struct {
	Name string
	Args []string
	Body []Clause
}

// RuleCall is a Where-clause that invokes a Rule by name. Args are the
// terms passed to the rule head — typically a mix of bound variables
// from the surrounding query and literal anchors. MemoryStore resolves
// the call via fixed-point evaluation; Datalevin treats it as a
// rule-form predicate.
type RuleCall struct {
	Name string
	Args []Term
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
//
// Attribute, when non-empty, scopes the search to a single attribute
// — renders as `(fulltext $ :attr "query")` — which is faster on the
// Datalevin side when the attribute is `:db.fulltext/autoDomain
// true`.
//
// Expr, when non-empty, replaces the quoted Query literal with a raw
// Datalog query expression (e.g. `[:and {:phrase "little lamb"}
// "fleece"]`). The caller owns the syntax; the renderer drops it in
// verbatim. Mutually exclusive with Query — set one or the other.
type FullText struct {
	Entity    Term
	Query     string
	Attribute string
	Expr      string
}

func (*Pattern) clauseNode()   {}
func (*Predicate) clauseNode() {}
func (*Or) clauseNode()        {}
func (*Not) clauseNode()       {}
func (*FullText) clauseNode()  {}
func (*RuleCall) clauseNode()  {}

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

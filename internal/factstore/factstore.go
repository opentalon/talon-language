package factstore

import (
	"context"
	"time"
)

// FactStore is the database abstraction layer. All compiler and runtime code
// targets this interface — never a concrete backend directly.
type FactStore interface {
	Assert(ctx context.Context, entityID string, facts []Fact) error
	Retract(ctx context.Context, entityID string, pattern FactPattern) error
	Query(ctx context.Context, entityID string, query FactQuery) ([]FactResult, error)
	QueryRecursive(ctx context.Context, entityID string, query RecursiveQuery) ([]FactResult, error)
	Aggregate(ctx context.Context, entityID string, query AggregateQuery) (AggregateResult, error)
	QueryAsOf(ctx context.Context, entityID string, query FactQuery, asOf time.Time) ([]FactResult, error)
	Transact(ctx context.Context, entityID string, ops []TransactOp) error
	CreateEntity(ctx context.Context, entityID string) error
	DropEntity(ctx context.Context, entityID string) error
}

// Fact is a single entity-attribute-value triple with timestamp.
type Fact struct {
	Entity    string
	RecordID  string
	Attribute string
	Value     any
	Timestamp time.Time
}

// FactPattern matches facts by field. A nil pointer means "match any".
// Value nil means match any value; a concrete value means exact match.
type FactPattern struct {
	Entity    *string
	RecordID  *string
	Attribute *string
	Value     any
}

type FactQuery struct {
	Patterns []FactPattern
	Negation []FactPattern
	OrderBy  string
	Limit    int
}

type FactResult struct {
	Fact Fact
}

// RecursiveQuery traverses EAV graphs (category trees, manager chains).
type RecursiveQuery struct {
	Start         FactPattern
	EdgeAttribute string
	MaxDepth      int // 0 = unlimited
	Direction     TraversalDirection
}

type TraversalDirection int

const (
	TraverseDown TraversalDirection = iota
	TraverseUp
	TraverseBoth
)

type AggregateQuery struct {
	Patterns  []FactPattern
	Attribute string
	Function  AggregateFunction
	GroupBy   string
}

type AggregateFunction int

const (
	AggCount  AggregateFunction = iota
	AggSum    AggregateFunction = iota
	AggAvg    AggregateFunction = iota
	AggMin    AggregateFunction = iota
	AggMax    AggregateFunction = iota
	AggStddev AggregateFunction = iota
)

type AggregateResult struct {
	Value  float64
	Count  int
	Groups map[string]float64
}

type TransactOp struct {
	Kind    TransactKind
	Facts   []Fact
	Pattern *FactPattern
}

type TransactKind int

const (
	TransactAssert  TransactKind = iota
	TransactRetract TransactKind = iota
)

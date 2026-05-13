package planner

import (
	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/diagnostic"
)

// PlanStepKind identifies the type of a plan step.
type PlanStepKind int

const (
	StepFactStoreQuery    PlanStepKind = iota
	StepFactStoreAggregate
	StepFactStoreRecursive
	StepGoComputation
	StepMLPrimitive
	StepFilter
)

// PlanStep is one unit of work in an evaluated rule's execution plan.
type PlanStep struct {
	Kind PlanStepKind
	// Payload is one of FactStoreQuery, GoComputation, MLPrimitive, or Filter;
	// the concrete type depends on Kind.
	Payload interface{}
}

// Plan compiles a validated AST into a sequence of PlanSteps per block.
func Plan(prog *ast.Program) (map[string][]PlanStep, diagnostic.List) {
	panic("not implemented")
}

package mlruntime

import "fmt"

// Registry holds the active ML primitives, keyed by planner function name
// (e.g. planner.FuncAnomalyZscore). The executor and testrunner dispatch
// MLComputation steps through it.
type Registry struct {
	primitives map[string]Primitive
}

// NewRegistry returns a Registry pre-populated with all built-in primitives.
func NewRegistry() *Registry {
	r := &Registry{primitives: map[string]Primitive{}}
	r.Register(NewZScoreAnomaly())
	r.Register(NewLearnedThreshold())
	return r
}

// Register adds or replaces a primitive.
func (r *Registry) Register(p Primitive) {
	r.primitives[p.Name()] = p
}

// Get returns the primitive bound to fn, or an error if none is registered.
func (r *Registry) Get(fn string) (Primitive, error) {
	p, ok := r.primitives[fn]
	if !ok {
		return nil, fmt.Errorf("no ml primitive registered for %q", fn)
	}
	return p, nil
}

// Has reports whether fn has a primitive registered.
func (r *Registry) Has(fn string) bool {
	_, ok := r.primitives[fn]
	return ok
}

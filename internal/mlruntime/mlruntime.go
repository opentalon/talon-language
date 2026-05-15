// Package mlruntime executes the 7 ML primitive keywords described in
// ADR-0001. Each primitive returns a (value, Explanation) pair so labels
// and `talon trace` can render the decision path.
//
// Primitives are registered in Registry (see registry.go) and looked up by
// the planner.FuncXxx constants. Callers construct a registry with
// NewRegistry() and dispatch MLComputation steps through Registry.Get.
package mlruntime

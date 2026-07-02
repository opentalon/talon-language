// Package talon is the public Go SDK for the Talon language.
//
// It exposes the compile + execute pipeline that the talon CLI uses
// internally, so other Go programs (in particular the OpenTalon
// talon-plugin) can run Talon workflow source against a host-supplied
// MCP caller without depending on internal packages.
//
// This package currently supports workflow-only programs — those whose
// blocks consist of MCP step chains and pure Go computations. Programs
// that contain Datalevin-backed queries or ML primitives require a live
// Datalevin client and are out of scope for [RunWorkflow]; a separate
// entry point may be added later.
//
// Minimal example:
//
//	caller := myMCPCaller{...}              // implements talon.MCPCaller
//	result, err := talon.RunWorkflow(ctx, src, talon.WithMCP(caller))
//	if err != nil { ... }
//	for name, block := range result.Blocks {
//	    fmt.Println(name, "→", len(block.Steps), "steps")
//	}
//
// For validating source without executing it — e.g. checking
// machine-generated agent source and reporting diagnostics back for
// correction — use [Check]. The [Fact], [Event], and [RetractPattern]
// types (with the Event* kind constants) let external code build facts
// and drive a store obtained via [NewMemoryStore] / [NewFactStore]
// without importing internal packages.
package talon

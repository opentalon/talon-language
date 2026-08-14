// Package tln is the public Go SDK for the tln language.
//
// It exposes the compile + execute pipeline that the tln CLI uses
// internally, so other Go programs (in particular the OpenTalon
// tln-plugin) can run tln workflow source against a host-supplied
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
//	caller := myToolResolver{...}              // implements tln.ToolResolver
//	result, err := tln.RunWorkflow(ctx, src, tln.WithToolResolver(caller))
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
//
// For long-lived, event-driven use — a watcher agent that holds a
// program and reacts as facts arrive — use [NewSession]. Assert facts
// into the session and its `on` blocks fire their referenced workflows,
// returning the [Firing] list for each mutation.
package tln

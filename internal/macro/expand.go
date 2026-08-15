// Package macro is tln's compile-time macro-expansion phase (ADR 0011).
//
// Metaprogramming in tln follows the Elixir model: macros expand at COMPILE
// time into ordinary AST, and the runtime never sees a macro or a code-as-data
// term. [Expand] runs between import resolution and validation in the compile
// pipeline (see pkg/tln compileProgram); it rewrites macro invocations to a
// fixpoint and hands the validator and planner a program of ordinary blocks.
//
// This placement is why metaprogramming belongs in *core* rather than a plugin:
// only core owns the grammar and the compile phases, and expanding-to-ordinary-
// AST keeps the runtime resolver flat, deterministic, and terminating. The one
// place tln admits unbounded computation is expansion itself, which is bounded
// by [MaxExpansionSteps] — a compile-time failure, not a runtime-semantics
// change.
package macro

import (
	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/diagnostic"
)

// MaxExpansionSteps bounds macro expansion so a self-triggering macro fails at
// compile time (a diagnostic) instead of looping forever. The runtime stays
// terminating because it only ever sees fully-expanded, ordinary AST.
const MaxExpansionSteps = 10000

// Expand is the macro-expansion phase: it rewrites `defmacro` invocations in
// prog to a fixpoint (bounded by [MaxExpansionSteps]) and returns a program of
// ordinary AST blocks for the validator and planner to consume unchanged.
//
// STATUS: insertion seam only. The `defmacro`/`quote`/`unquote` grammar and the
// rewrite engine are the subject of ADR 0011; until they land, Expand is the
// identity transform. Wiring it into the pipeline now proves the phase's place
// (post-import, pre-validate) with the whole suite still green, so the language
// work lands as a fill-in rather than a pipeline change.
func Expand(file string, prog *ast.Program) (*ast.Program, diagnostic.List) {
	// No `defmacro` in the grammar yet, so there is nothing to expand: every
	// block is already ordinary AST. When the grammar lands, this is where the
	// fixpoint rewrite runs, emitting ordinary blocks and dropping macro
	// definitions from the output.
	return prog, nil
}

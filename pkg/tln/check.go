package tln

import "github.com/opentalon/tln-language/internal/ast"

// Check compiles a tln source string without executing it, running
// the full compile pipeline:
//
//	lex → parse → resolve imports → validate → plan
//
// It returns nil when the source is valid and a *CompileError carrying
// the failing stage and its diagnostics otherwise. No FactStore, MCP
// caller, or execution is involved, so Check is safe for validating
// untrusted or machine-generated source (e.g. an LLM authoring an
// agent) and reporting the diagnostics back for correction.
//
// Check accepts the same [Option] values as [Run]; only [WithFilename]
// affects it (labelling diagnostics and setting the base path for
// relative imports). Execution-only options such as [WithToolResolver] and
// [WithFactStore] are accepted but ignored.
func Check(src string, opts ...Option) error {
	cfg := &runConfig{file: "<tln>"}
	for _, opt := range opts {
		opt(cfg)
	}
	if _, err := compile(cfg.file, src); err != nil {
		return err
	}
	return nil
}

// HasReactiveRules reports whether the tln source contains reactive rules —
// `on` blocks or `detect` blocks — as opposed to a purely imperative program
// (top-level `workflow` blocks only). Callers use this to route a domain event:
// a reactive program is evaluated against the asserted facts (so the matched
// record binds in interpolation and its on/detect rules fire), while a
// workflow-only program is run imperatively. It runs the same compile pipeline
// as [Check] and returns the compile error unchanged for invalid source.
func HasReactiveRules(src string, opts ...Option) (bool, error) {
	cfg := &runConfig{file: "<tln>"}
	for _, opt := range opts {
		opt(cfg)
	}
	prog, _, err := compileProgram(cfg.file, src)
	if err != nil {
		return false, err
	}
	for _, b := range prog.Blocks {
		switch b.(type) {
		case *ast.DetectBlock, *ast.OnBlock:
			return true, nil
		}
	}
	return false, nil
}

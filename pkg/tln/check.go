package tln

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
// relative imports). Execution-only options such as [WithMCP] and
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

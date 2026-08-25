package tln

import (
	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/lexer"
	"github.com/opentalon/tln-language/internal/parser"
	"github.com/opentalon/tln-language/internal/testrunner"
)

// TestResult is the outcome of one `test` block, as run by [RunTests].
// Aliased from the internal testrunner so callers outside this module
// don't need it either.
type TestResult = testrunner.TestResult

// RunTests compiles a tln ruleset and a paired .tln.test source and runs
// every test block in testSource against it. Mirrors `tln test`
// (cmd/tln/main.go's runTestPair) minus the CLI's file I/O, for hosts that
// need to run a tenant's tests without shelling out to the binary — e.g. a
// server-side "validate this ruleset" action.
//
// rulesSource goes through the same pipeline [Check] and [Run] use: lex →
// parse → resolve imports → macro-expand → validate → plan. testSource is
// only lexed and parsed (test blocks carry no imports or macros of their
// own), then its blocks are appended to the compiled rules program so
// did / did_not assertions can resolve against the rule blocks — a test
// file alone doesn't carry those.
//
// Returns a *CompileError for a lex/parse failure in either source, a
// validate failure in rulesSource, or a merged-program validation failure
// (e.g. a test referencing an unknown block, or asserting a verb the rule
// under test never does). A successful compile returns one TestResult per
// test block; RunTests accepts the same [Option] values as [Check] and
// [Run], though only [WithFilename] (labelling rulesSource) applies.
func RunTests(rulesSource, testSource string, opts ...Option) ([]TestResult, error) {
	cfg := &runConfig{file: "<tln>"}
	for _, opt := range opts {
		opt(cfg)
	}

	prog, plans, err := compileProgram(cfg.file, rulesSource)
	if err != nil {
		return nil, err
	}

	const testFile = "<tln.test>"
	tokens, lexDiags := lexer.Lex(testFile, testSource)
	if lexDiags.HasErrors() {
		return nil, &CompileError{Stage: "lex", Diags: lexDiags}
	}
	testProg, parseDiags := parser.Parse(testFile, tokens)
	if parseDiags.HasErrors() {
		return nil, &CompileError{Stage: "parse", Diags: parseDiags}
	}

	merged := *prog
	merged.Blocks = append(append([]ast.Block{}, prog.Blocks...), testProg.Blocks...)
	if valDiags := testrunner.Validate(&merged, plans); valDiags.HasErrors() {
		return nil, &CompileError{Stage: "validate", Diags: valDiags}
	}

	return testrunner.Run(&merged, plans), nil
}

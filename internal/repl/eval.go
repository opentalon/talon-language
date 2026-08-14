package repl

import (
	"fmt"
	"sort"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/diagnostic"
	"github.com/opentalon/tln-language/internal/lexer"
	"github.com/opentalon/tln-language/internal/parser"
	"github.com/opentalon/tln-language/internal/planner"
	"github.com/opentalon/tln-language/internal/testrunner"
	"github.com/opentalon/tln-language/internal/validator"
)

// evalResult carries the runtime output of evaluating one block.
type evalResult struct {
	BlockName string
	Flagged   []int
	Steps     []testrunner.TraceStep
}

// compile lex/parse/validate/plans the given program. Diagnostics with
// severity Error abort; other diagnostics are returned for the caller to
// surface (the REPL prints them as warnings).
func compileProgram(prog *ast.Program) (map[string]*planner.QueryPlan, diagnostic.List, error) {
	var diags diagnostic.List

	// Validate
	vd := validator.Validate("<repl>", prog)
	diags = append(diags, vd...)
	if vd.HasErrors() {
		return nil, diags, fmt.Errorf("validation failed")
	}

	// Plan
	plans, planDiags := planner.Plan(prog)
	diags = append(diags, planDiags...)
	if planDiags.HasErrors() {
		return nil, diags, fmt.Errorf("planning failed")
	}
	return plans, diags, nil
}

// parseSource lexes + parses a tln source snippet. Lex/parse errors are
// returned as a diagnostic list, so the REPL can show them with file/line
// context and recover.
func parseSource(src string) (*ast.Program, diagnostic.List) {
	var diags diagnostic.List
	tokens, ld := lexer.Lex("<repl>", src)
	diags = append(diags, ld...)
	prog, pd := parser.Parse("<repl>", tokens)
	diags = append(diags, pd...)
	return prog, diags
}

// evalBlock evaluates the session block named target against the session's
// facts. It synthesises a TestBlock so the testrunner's in-memory path runs
// unchanged — no new evaluator, no FactStore implementation needed.
func evalBlock(s *Session, target string) (*evalResult, diagnostic.List, error) {
	if s.BlockByName(target) == nil {
		return nil, nil, fmt.Errorf("no block named %q in session — use :rules to list", target)
	}
	prog := s.Program()
	synthName := "__repl_eval__" + target
	prog.Blocks = append(prog.Blocks, &ast.TestBlock{
		Name:      synthName,
		Given:     append([]ast.TestDatum(nil), s.Facts...),
		WhenKind:  "detect",
		WhenBlock: target,
	})

	plans, diags, err := compileProgram(prog)
	if err != nil {
		return nil, diags, err
	}

	traces := testrunner.Trace(prog, plans)
	for _, t := range traces {
		if t.Name == synthName {
			return &evalResult{
				BlockName: target,
				Flagged:   t.Flagged,
				Steps:     t.Steps,
			}, diags, nil
		}
	}
	return nil, diags, fmt.Errorf("no trace produced for %q (block kind may not yet be plannable)", target)
}

// findRecords evaluates a `for records where <selector-conditions>` fragment
// against the session's facts and returns the matching record IDs. We
// implement this by synthesising a one-off `detect` block, then reusing the
// same evaluator path as :eval.
func findRecords(s *Session, selectorFragment string) ([]int, diagnostic.List, error) {
	// The grammar requires a flag clause for a complete detect block; add a
	// placeholder so the synthesized block validates.
	src := fmt.Sprintf(`detect "%s" {
  %s
  flag matching items
}`, replFindName, selectorFragment)

	tinyProg, parseDiags := parseSource(src)
	if parseDiags.HasErrors() {
		return nil, parseDiags, fmt.Errorf("could not parse selector — expected `for records where <condition>`")
	}
	if len(tinyProg.Blocks) == 0 {
		return nil, parseDiags, fmt.Errorf("no block produced from selector")
	}

	// Build a one-shot session with just the synthesized block, then eval.
	tmp := NewSession()
	tmp.Facts = append([]ast.TestDatum(nil), s.Facts...)
	tmp.Blocks = []ast.Block{tinyProg.Blocks[0]}

	res, evalDiags, err := evalBlock(tmp, replFindName)
	allDiags := append(parseDiags, evalDiags...)
	if err != nil {
		return nil, allDiags, err
	}
	sort.Ints(res.Flagged)
	return res.Flagged, allDiags, nil
}

// replFindName is the synthesised block name used by :find / :count.
const replFindName = "__repl_find__"

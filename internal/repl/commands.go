package repl

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/explain"
	"github.com/opentalon/talon-language/internal/testrunner"
)

// runCommand executes a slash command and writes output to w. It returns
// done=true when the user asked to quit.
func runCommand(s *Session, line string, w io.Writer) (done bool) {
	rest := strings.TrimSpace(strings.TrimPrefix(line, ":"))
	if rest == "" {
		fmt.Fprintln(w, "  empty command; try :help")
		return false
	}
	head, tail := splitFirstWord(rest)
	switch head {
	case "quit", "exit", "q":
		return true
	case "help":
		printHelp(w)
	case "facts":
		printFacts(s, w)
	case "rules":
		printRules(s, w)
	case "context":
		runContext(s, tail, w)
	case "clear":
		runClear(s, tail, w)
	case "load":
		runLoad(s, tail, w)
	case "eval":
		runEval(s, tail, w)
	case "trace":
		runTrace(s, tail, w)
	case "find":
		runFind(s, tail, w, false)
	case "count":
		runFind(s, tail, w, true)
	case "why":
		runWhy(s, tail, w)
	default:
		fmt.Fprintf(w, "  unknown command :%s — try :help\n", head)
	}
	return false
}

func splitFirstWord(s string) (head, tail string) {
	s = strings.TrimSpace(s)
	for i, r := range s {
		if r == ' ' || r == '\t' {
			return s[:i], strings.TrimSpace(s[i:])
		}
	}
	return s, ""
}

func printHelp(w io.Writer) {
	const help = `  commands:
    :facts                  list facts in memory
    :rules                  list compiled blocks
    :eval "name"            evaluate one block
    :eval all               evaluate every detect/rule block
    :trace "name"           evaluate with step-by-step trace
    :why "block" [id]       show the decision chain backing a flag
                            e.g. :why "Service overdue" 501
                            id-only form lists every block that flagged it
    :find <selector>        return matching record IDs
                            e.g. :find for records where type == "item"
    :count <selector>       like :find but just the count
    :context KEY VALUE      set a context variable (e.g. :context role "manager")
    :load FILE              load .talon source from a file; a program with
                            'on' blocks becomes a live watcher — assert
                            facts and matching on-blocks fire their workflows
    :clear                  drop all facts, blocks, and context
    :clear facts            drop facts but keep blocks
    :help                   show this help
    :quit                   exit the REPL
`
	fmt.Fprint(w, help)
}

func printFacts(s *Session, w io.Writer) {
	if len(s.Facts) == 0 {
		fmt.Fprintln(w, "  no facts in memory")
		return
	}
	// Group facts by record ID so the printout is by entity, not by line.
	byID := map[int][]ast.TestDatum{}
	for _, f := range s.Facts {
		byID[f.ID] = append(byID[f.ID], f)
	}
	ids := make([]int, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	fmt.Fprintf(w, "  %d records in memory:\n", len(ids))
	for _, id := range ids {
		// Print the `record` line first if present.
		for _, f := range byID[id] {
			if f.Kind != "record" {
				continue
			}
			fmt.Fprintf(w, "    record %d", id)
			for _, k := range sortedKeys(f.Fields) {
				fmt.Fprintf(w, " %s %s", k, formatValue(f.Fields[k]))
			}
			fmt.Fprintln(w)
		}
		// Then attrs in stable order.
		for _, f := range byID[id] {
			if f.Kind != "attr" {
				continue
			}
			for _, k := range sortedKeys(f.Fields) {
				fmt.Fprintf(w, "    attr %d %q %s\n", id, k, formatValue(f.Fields[k]))
			}
		}
	}
}

func sortedKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func formatValue(v interface{}) string {
	switch vv := v.(type) {
	case string:
		return fmt.Sprintf("%q", vv)
	case float64:
		// Render whole-number floats without a decimal.
		if vv == float64(int64(vv)) {
			return fmt.Sprintf("%d", int64(vv))
		}
		return fmt.Sprintf("%g", vv)
	case bool:
		return fmt.Sprintf("%t", vv)
	}
	return fmt.Sprintf("%v", v)
}

func printRules(s *Session, w io.Writer) {
	names := s.BlockNames()
	if len(names) == 0 {
		fmt.Fprintln(w, "  no blocks compiled in this session")
		return
	}
	fmt.Fprintf(w, "  %d block(s):\n", len(names))
	for _, n := range names {
		b := s.BlockByName(n)
		fmt.Fprintf(w, "    %-12s  %q\n", blockKind(b), n)
	}
}

func runContext(s *Session, tail string, w io.Writer) {
	if tail == "" {
		if len(s.Context) == 0 {
			fmt.Fprintln(w, "  no context set")
			return
		}
		for _, k := range sortedKeys(asStringMap(s.Context)) {
			fmt.Fprintf(w, "    %s = %q\n", k, s.Context[k])
		}
		return
	}
	key, rest := splitFirstWord(tail)
	if key == "" || rest == "" {
		fmt.Fprintln(w, `  usage: :context KEY VALUE`)
		return
	}
	// Allow quoted value: strip a single pair of surrounding quotes.
	if len(rest) >= 2 && rest[0] == '"' && rest[len(rest)-1] == '"' {
		rest = rest[1 : len(rest)-1]
	}
	s.Context[key] = rest
	fmt.Fprintf(w, "  context.%s = %q\n", key, rest)
}

func asStringMap(m map[string]string) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func runClear(s *Session, tail string, w io.Writer) {
	switch tail {
	case "", "all":
		s.ClearAll()
		fmt.Fprintln(w, "  cleared facts, blocks, and context")
	case "facts":
		s.ClearFacts()
		fmt.Fprintln(w, "  cleared facts; blocks kept")
	default:
		fmt.Fprintf(w, "  unknown :clear target %q (try :clear or :clear facts)\n", tail)
	}
}

func runLoad(s *Session, tail string, w io.Writer) {
	if tail == "" {
		fmt.Fprintln(w, "  usage: :load PATH")
		return
	}
	// Strip surrounding quotes if the user typed them.
	path := strings.Trim(tail, `"`)
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(w, "  :load: %v\n", err)
		return
	}
	prog, diags := parseSource(string(src))
	printDiagnostics(diags, w)
	if diags.HasErrors() {
		fmt.Fprintf(w, "  :load: refused to merge %q (parse errors)\n", path)
		return
	}
	// Append blocks; collect any test data so the REPL can use loaded
	// fixtures as facts.
	added := 0
	factsAdded := 0
	onBlocks := 0
	for _, b := range prog.Blocks {
		if tb, ok := b.(*ast.TestBlock); ok {
			for _, d := range tb.Given {
				s.AddFact(d)
				factsAdded++
			}
			continue
		}
		if _, ok := b.(*ast.OnBlock); ok {
			onBlocks++
		}
		s.AddBlocks([]ast.Block{b})
		added++
	}
	fmt.Fprintf(w, "  loaded %s: %d block(s), %d fact(s)\n", path, added, factsAdded)

	// A program with `on` blocks becomes a live watcher: build a reactive
	// session from the file source so asserting facts fires the on-blocks.
	if onBlocks > 0 {
		if err := s.startWatch(string(src), w); err != nil {
			fmt.Fprintf(w, "  watch: %v\n", err)
			return
		}
		fmt.Fprintf(w, "  watching: %d on-block(s) armed — assert facts to fire them\n", onBlocks)
	}
}

func runEval(s *Session, tail string, w io.Writer) {
	if tail == "" {
		fmt.Fprintln(w, `  usage: :eval "name" | :eval all`)
		return
	}
	if tail == "all" {
		for _, name := range s.BlockNames() {
			b := s.BlockByName(name)
			if !isEvalKind(b) {
				continue
			}
			evalOne(s, name, w, false)
		}
		return
	}
	name := strings.Trim(tail, `"`)
	evalOne(s, name, w, false)
}

func runTrace(s *Session, tail string, w io.Writer) {
	if tail == "" {
		fmt.Fprintln(w, `  usage: :trace "name"`)
		return
	}
	name := strings.Trim(tail, `"`)
	evalOne(s, name, w, true)
}

// isEvalKind reports whether a block produces detection results that are
// meaningful to print from `:eval all`. Defines / workflows / constraints /
// on-blocks are skipped.
func isEvalKind(b ast.Block) bool {
	switch b.(type) {
	case *ast.DetectBlock, *ast.RuleBlock, *ast.RecommendBlock,
		*ast.PredictBlock, *ast.ForecastBlock, *ast.ClusterBlock,
		*ast.ClassifyBlock, *ast.SimilarBlock, *ast.RelatedBlock:
		return true
	}
	return false
}

func evalOne(s *Session, name string, w io.Writer, trace bool) {
	res, diags, err := evalBlock(s, name)
	printDiagnostics(diags, w)
	if err != nil {
		fmt.Fprintf(w, "  :eval %q: %v\n", name, err)
		return
	}
	if len(res.Flagged) == 0 {
		fmt.Fprintf(w, "  %q: 0 detections\n", name)
	} else {
		ids := append([]int(nil), res.Flagged...)
		sort.Ints(ids)
		fmt.Fprintf(w, "  %q: %d detection(s) — records %v\n", name, len(ids), ids)
	}
	if trace {
		printSteps(res, w)
	}
}

func printSteps(res *evalResult, w io.Writer) {
	if len(res.Steps) == 0 {
		fmt.Fprintln(w, "    (no steps recorded)")
		return
	}
	fmt.Fprintln(w, "    trace:")
	for i, st := range res.Steps {
		label := st.Type
		if st.Function != "" {
			label += " " + st.Function
		}
		fmt.Fprintf(w, "    step %d  %s → %s", i+1, label, st.Into)
		if len(st.Rows) > 0 {
			fmt.Fprintf(w, "  (rows: %v)", st.Rows)
		}
		fmt.Fprintln(w)
	}
}

func runFind(s *Session, tail string, w io.Writer, countOnly bool) {
	if tail == "" {
		fmt.Fprintln(w, "  usage: :find <selector>  (e.g. :find for records where type == \"item\")")
		return
	}
	// Allow the user to type either the full `for records where ...` form
	// or shorthand `where ...` / a bare condition. Normalize to the form
	// the grammar requires.
	frag := strings.TrimSpace(tail)
	switch {
	case strings.HasPrefix(frag, "for records where"):
		// already canonical
	case strings.HasPrefix(frag, "for records"):
		// trailing `where` likely missing
		fmt.Fprintln(w, "  expected `for records where <condition>`")
		return
	case strings.HasPrefix(frag, "where"):
		frag = "for records " + frag
	default:
		frag = "for records where " + frag
	}

	ids, diags, err := findRecords(s, frag)
	printDiagnostics(diags, w)
	if err != nil {
		fmt.Fprintf(w, "  :find: %v\n", err)
		return
	}
	if countOnly {
		fmt.Fprintf(w, "  %d\n", len(ids))
		return
	}
	if len(ids) == 0 {
		fmt.Fprintln(w, "  no matching records")
		return
	}
	for _, id := range ids {
		fmt.Fprintf(w, "  %d\n", id)
	}
}

// runWhy answers "why did this block flag this entity?" interactively.
// Shapes:
//
//	:why "Block Name"             — every flag this block produced, with chain
//	:why "Block Name" 501         — only the flag on entity 501
//	:why 501                      — every block that flagged 501
//
// Internally synthesises a TestBlock (same pattern as :eval) so the
// testrunner's Decision-chain pipeline runs unchanged, then filters
// the resulting Decisions by block name and/or entity ID. Reuses
// the CLI's `talon why` plumbing semantically — same Decision +
// explain.RenderAll output format — so users learning one surface
// know the other.
func runWhy(s *Session, tail string, w io.Writer) {
	if tail == "" {
		fmt.Fprintln(w, `  usage: :why "block name" [entity-id]   |   :why <entity-id>`)
		return
	}

	blockName, entityID, err := parseWhyArgs(tail)
	if err != nil {
		fmt.Fprintf(w, "  :why: %v\n", err)
		return
	}
	// Need at least one anchor — a block to chain back from, or an
	// entity to look up. Matches the CLI's same-named guard.
	if blockName == "" && entityID < 0 {
		fmt.Fprintln(w, "  :why: provide a block name (quoted) or an entity ID")
		return
	}

	// Determine which session blocks to evaluate. If the user gave a
	// block name, just that one; if they gave only an entity ID, walk
	// every eval-able block (detect/rule/etc.) so we find every
	// flagger of that entity.
	var targets []ast.Block
	if blockName != "" {
		b := s.BlockByName(blockName)
		if b == nil {
			fmt.Fprintf(w, "  :why: no block named %q in session — use :rules to list\n", blockName)
			return
		}
		targets = []ast.Block{b}
	} else {
		for _, b := range s.Blocks {
			if isEvalKind(b) {
				targets = append(targets, b)
			}
		}
		if len(targets) == 0 {
			fmt.Fprintln(w, "  :why: no evaluable blocks in session")
			return
		}
	}

	// One synthetic TestBlock per target so the testrunner's Decisions
	// pipeline produces a chain anchored on a known test name. We
	// merge the results below and filter by block/entity.
	prog := s.Program()
	testNames := make([]string, 0, len(targets))
	for _, b := range targets {
		synthName := "__repl_why__" + b.BlockName()
		prog.Blocks = append(prog.Blocks, &ast.TestBlock{
			Name:      synthName,
			Given:     append([]ast.TestDatum(nil), s.Facts...),
			WhenKind:  "detect",
			WhenBlock: b.BlockName(),
		})
		testNames = append(testNames, synthName)
	}

	plans, diags, err := compileProgram(prog)
	printDiagnostics(diags, w)
	if err != nil {
		fmt.Fprintf(w, "  :why: %v\n", err)
		return
	}
	decisions := testrunner.Decisions(prog, plans)

	// Filter: keep only decisions for the synthesised tests we just
	// added, and within those, only ones matching the user's anchors.
	wantTest := map[string]bool{}
	for _, n := range testNames {
		wantTest[n] = true
	}
	var filtered []explain.Decision
	for name, ds := range decisions {
		if !wantTest[name] {
			continue
		}
		for _, d := range ds {
			if !decisionMatches(d, blockName, entityID) {
				continue
			}
			filtered = append(filtered, d)
		}
	}

	if len(filtered) == 0 {
		switch {
		case blockName != "" && entityID >= 0:
			fmt.Fprintf(w, "  :why: %q didn't flag entity %d\n", blockName, entityID)
		case blockName != "":
			fmt.Fprintf(w, "  :why: %q flagged nothing\n", blockName)
		default:
			fmt.Fprintf(w, "  :why: nothing flagged entity %d\n", entityID)
		}
		return
	}

	fmt.Fprintln(w, explain.RenderAll(filtered))
}

// decisionMatches reports whether a decision satisfies the user's
// (blockName, entityID) anchors. A chain is a match if any node in
// it (the leaf Decision OR anything in TriggeredBy, recursively)
// satisfies all of the supplied filters. Walking the chain matters
// because the testrunner anchors output at the most-downstream block
// (the `recommend` after a `detect`, for example) — a user asking
// "why did Service overdue flag 501" expects a hit even when
// Service overdue is the *upstream* trigger of a recommend chain.
func decisionMatches(d explain.Decision, blockName string, entityID int) bool {
	if (blockName == "" || d.BlockName == blockName) &&
		(entityID < 0 || d.EntityID == entityID) {
		return true
	}
	for _, up := range d.TriggeredBy {
		if decisionMatches(up, blockName, entityID) {
			return true
		}
	}
	return false
}

// parseWhyArgs splits a `:why` tail into a (blockName, entityID) pair.
// Accepts three shapes:
//
//	"Block name"          → ("Block name", -1)
//	"Block name" 501      → ("Block name", 501)
//	501                   → ("", 501)
//
// Returns entityID = -1 when not supplied (the caller treats that as
// "no entity filter"). An unparseable tail returns an error with a
// hint at the right shape.
func parseWhyArgs(tail string) (block string, entityID int, err error) {
	tail = strings.TrimSpace(tail)
	entityID = -1
	if tail == "" {
		return "", -1, fmt.Errorf("empty arguments")
	}

	// Quoted block name comes first when present.
	if strings.HasPrefix(tail, `"`) {
		end := strings.Index(tail[1:], `"`)
		if end < 0 {
			return "", -1, fmt.Errorf("unterminated block name (missing closing quote)")
		}
		block = tail[1 : end+1]
		rest := strings.TrimSpace(tail[end+2:])
		if rest != "" {
			n, perr := strconv.Atoi(rest)
			if perr != nil {
				return "", -1, fmt.Errorf("expected integer entity ID after block name, got %q", rest)
			}
			entityID = n
		}
		return block, entityID, nil
	}

	// No quote — must be a bare entity ID.
	n, perr := strconv.Atoi(tail)
	if perr != nil {
		return "", -1, fmt.Errorf(`expected "block name" [entity-id] or <entity-id>, got %q`, tail)
	}
	return "", n, nil
}

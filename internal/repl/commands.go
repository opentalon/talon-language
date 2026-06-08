package repl

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/opentalon/talon-language/internal/ast"
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
    :find <selector>        return matching record IDs
                            e.g. :find for records where type == "item"
    :count <selector>       like :find but just the count
    :context KEY VALUE      set a context variable (e.g. :context role "manager")
    :load FILE              load .talon source from a file
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
	for _, b := range prog.Blocks {
		if tb, ok := b.(*ast.TestBlock); ok {
			for _, d := range tb.Given {
				s.AddFact(d)
				factsAdded++
			}
			continue
		}
		s.AddBlocks([]ast.Block{b})
		added++
	}
	fmt.Fprintf(w, "  loaded %s: %d block(s), %d fact(s)\n", path, added, factsAdded)
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

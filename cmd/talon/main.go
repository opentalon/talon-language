package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opentalon/talon-language/internal/datalevin"
	"github.com/opentalon/talon-language/internal/diagnostic"
	"github.com/opentalon/talon-language/internal/executor"
	"github.com/opentalon/talon-language/internal/lexer"
	"github.com/opentalon/talon-language/internal/parser"
	"github.com/opentalon/talon-language/internal/planner"
	"github.com/opentalon/talon-language/internal/testrunner"
	"github.com/opentalon/talon-language/internal/validator"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: talon <command> [args]")
		fmt.Fprintln(os.Stderr, "commands: build, test, run, repl, trace, mod")
		os.Exit(diagnostic.ExitUsage)
	}

	switch os.Args[1] {
	case "build":
		runBuild()
	case "test":
		runTest()
	case "run":
		runExecute()
	case "repl":
		fmt.Fprintln(os.Stderr, "talon repl: not yet implemented")
		os.Exit(diagnostic.ExitError)
	case "trace":
		runTrace()
	case "mod":
		fmt.Fprintln(os.Stderr, "talon mod: not yet implemented")
		os.Exit(diagnostic.ExitError)
	default:
		fmt.Fprintf(os.Stderr, "talon: unknown command %q\n", os.Args[1])
		os.Exit(diagnostic.ExitUsage)
	}
}

func runBuild() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: talon build <file.talon>")
		os.Exit(diagnostic.ExitUsage)
	}

	path := os.Args[2]
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "talon build: %v\n", err)
		os.Exit(diagnostic.ExitError)
	}

	file := filepath.Base(path)
	var allDiags diagnostic.List

	// Lex
	tokens, ld := lexer.Lex(file, string(src))
	allDiags = append(allDiags, ld...)

	// Parse
	prog, pd := parser.Parse(file, tokens)
	allDiags = append(allDiags, pd...)

	// Validate
	vd := validator.Validate(file, prog)
	allDiags = append(allDiags, vd...)

	// Print warnings/errors
	for _, d := range allDiags {
		if d.Severity == diagnostic.Error {
			fmt.Fprintf(os.Stderr, "error: %s\n", d)
		} else if d.Severity == diagnostic.Warning {
			fmt.Fprintf(os.Stderr, "warn:  %s\n", d)
		}
	}

	if allDiags.HasErrors() {
		os.Exit(diagnostic.ExitError)
	}

	// Plan
	plans, planDiags := planner.Plan(prog)
	for _, d := range planDiags {
		fmt.Fprintf(os.Stderr, "error: %s\n", d)
	}
	if planDiags.HasErrors() {
		os.Exit(diagnostic.ExitError)
	}

	// Print summary
	fmt.Printf("==> %s: %d block(s) compiled\n", file, len(plans))

	// Sort block names for stable output
	names := make([]string, 0, len(plans))
	for name := range plans {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		plan := plans[name]
		fmt.Printf("\n  [%s] %d step(s)\n", plan.BlockName, len(plan.Steps))
		for i, step := range plan.Steps {
			printStep(i+1, step)
		}
	}
}

func runTest() {
	args := os.Args[2:]
	var paths []string
	runFilter := ""
	verbose := false
	junitOut := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-run" && i+1 < len(args):
			runFilter = args[i+1]
			i++
		case a == "-v":
			verbose = true
		case a == "--junit" && i+1 < len(args):
			junitOut = args[i+1]
			i++
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "talon test: unknown flag %q\n", a)
			fmt.Fprintln(os.Stderr, "usage: talon test [paths...] [-run NAME] [-v] [--junit FILE]")
			os.Exit(diagnostic.ExitUsage)
		default:
			paths = append(paths, a)
		}
	}

	pairs, err := resolveTestPairs(paths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "talon test: %v\n", err)
		os.Exit(diagnostic.ExitError)
	}
	if len(pairs) == 0 {
		fmt.Fprintln(os.Stderr, "talon test: no .talon.test files found")
		os.Exit(diagnostic.ExitError)
	}

	var suites []testrunner.JUnitSuite
	totalPassed, totalFailed := 0, 0
	for _, p := range pairs {
		results, ok := runTestPair(p.rules, p.test, runFilter, verbose)
		if !ok {
			os.Exit(diagnostic.ExitError)
		}
		suites = append(suites, testrunner.JUnitSuite{
			File:    filepath.Base(p.test),
			Results: results,
		})
		for _, r := range results {
			if r.Passed {
				totalPassed++
			} else {
				totalFailed++
			}
		}
	}
	fmt.Printf("\n%d passed, %d failed\n", totalPassed, totalFailed)

	if junitOut != "" {
		f, err := os.Create(junitOut)
		if err != nil {
			fmt.Fprintf(os.Stderr, "talon test: junit: %v\n", err)
			os.Exit(diagnostic.ExitError)
		}
		if err := testrunner.WriteJUnit(f, suites); err != nil {
			f.Close()
			fmt.Fprintf(os.Stderr, "talon test: junit: %v\n", err)
			os.Exit(diagnostic.ExitError)
		}
		if err := f.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "talon test: junit: %v\n", err)
			os.Exit(diagnostic.ExitError)
		}
	}

	if totalFailed > 0 {
		os.Exit(diagnostic.ExitError)
	}
}

type testPair struct {
	rules string
	test  string
}

// resolveTestPairs maps the user-supplied paths to rules/test file pairs.
// Two positional args of the form <rules.talon> <tests.talon.test> are paired
// directly so the legacy CLI keeps working. Otherwise each path is treated as
// a directory or `.talon.test` file; rules files are matched to tests by base
// name (`foo.talon` ↔ `foo.talon.test`).
func resolveTestPairs(paths []string) ([]testPair, error) {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	if len(paths) == 2 {
		a, b := paths[0], paths[1]
		ca, cb := classifyTalonPath(a), classifyTalonPath(b)
		if ca == "rules" && cb == "test" {
			return []testPair{{rules: a, test: b}}, nil
		}
		if ca == "test" && cb == "rules" {
			return []testPair{{rules: b, test: a}}, nil
		}
	}

	var rulesFiles, testFiles []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			switch classifyTalonPath(p) {
			case "rules":
				rulesFiles = append(rulesFiles, p)
			case "test":
				testFiles = append(testFiles, p)
			default:
				return nil, fmt.Errorf("not a .talon or .talon.test file: %s", p)
			}
			continue
		}
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			switch classifyTalonPath(path) {
			case "rules":
				rulesFiles = append(rulesFiles, path)
			case "test":
				testFiles = append(testFiles, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	rulesByBase := map[string]string{}
	for _, r := range rulesFiles {
		base := strings.TrimSuffix(filepath.Base(r), ".talon")
		rulesByBase[base] = r
	}

	var pairs []testPair
	for _, t := range testFiles {
		base := strings.TrimSuffix(filepath.Base(t), ".talon.test")
		r, ok := rulesByBase[base]
		if !ok {
			sibling := strings.TrimSuffix(t, ".test")
			if _, err := os.Stat(sibling); err == nil {
				r = sibling
			} else {
				return nil, fmt.Errorf("no rules file found for %s (need %s.talon nearby)", t, base)
			}
		}
		pairs = append(pairs, testPair{rules: r, test: t})
	}
	return pairs, nil
}

func classifyTalonPath(p string) string {
	switch {
	case strings.HasSuffix(p, ".talon.test"):
		return "test"
	case strings.HasSuffix(p, ".talon"):
		return "rules"
	}
	return ""
}

func runTestPair(rulesPath, testPath, filter string, verbose bool) ([]testrunner.TestResult, bool) {
	rulesSrc, err := os.ReadFile(rulesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "talon test: %v\n", err)
		return nil, false
	}
	rulesFile := filepath.Base(rulesPath)
	plans, ok := compile(rulesFile, string(rulesSrc))
	if !ok {
		return nil, false
	}

	testSrc, err := os.ReadFile(testPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "talon test: %v\n", err)
		return nil, false
	}
	testFile := filepath.Base(testPath)
	testTokens, tld := lexer.Lex(testFile, string(testSrc))
	if tld.HasErrors() {
		for _, d := range tld {
			fmt.Fprintf(os.Stderr, "error: %s\n", d)
		}
		return nil, false
	}
	testProg, tpd := parser.Parse(testFile, testTokens)
	if tpd.HasErrors() {
		for _, d := range tpd {
			fmt.Fprintf(os.Stderr, "error: %s\n", d)
		}
		return nil, false
	}
	if tvd := testrunner.Validate(testProg, plans); tvd.HasErrors() {
		for _, d := range tvd {
			fmt.Fprintf(os.Stderr, "error: %s\n", d)
		}
		return nil, false
	}

	results := testrunner.Run(testProg, plans)
	filtered := testrunner.FilterByName(results, filter)
	fmt.Printf("==> %s: %d test(s)\n", testFile, len(filtered))
	testrunner.PrintResults(filtered, verbose)
	return filtered, true
}

func runExecute() {
	// Parse args: talon run <file.talon> [--datalevin URL] [--seed file.talon.test]
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: talon run <file.talon> [--datalevin URL] [--seed file.talon.test]")
		os.Exit(diagnostic.ExitUsage)
	}

	path := os.Args[2]
	serverURL := "http://localhost:8898"
	seedPath := ""
	for i := 3; i < len(os.Args); i++ {
		if os.Args[i] == "--datalevin" && i+1 < len(os.Args) {
			serverURL = os.Args[i+1]
			i++
		} else if os.Args[i] == "--seed" && i+1 < len(os.Args) {
			seedPath = os.Args[i+1]
			i++
		}
	}

	// Compile
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "talon run: %v\n", err)
		os.Exit(diagnostic.ExitError)
	}

	file := filepath.Base(path)
	plans, ok := compile(file, string(src))
	if !ok {
		os.Exit(diagnostic.ExitError)
	}

	// Connect to Datalevin
	client := datalevin.NewClient(serverURL)
	ctx := context.Background()

	if err := client.Health(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "talon run: cannot reach datalevin-server at %s: %v\n", serverURL, err)
		fmt.Fprintln(os.Stderr, "hint: start the server with: cd datalevin-server && clj -M:run")
		os.Exit(diagnostic.ExitError)
	}

	exec := executor.NewExecutor(client)

	// Seed from .talon.test file if requested
	if seedPath != "" {
		seedSrc, err := os.ReadFile(seedPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "talon run: %v\n", err)
			os.Exit(diagnostic.ExitError)
		}
		seedFile := filepath.Base(seedPath)
		seedTokens, sld := lexer.Lex(seedFile, string(seedSrc))
		if sld.HasErrors() {
			for _, d := range sld {
				fmt.Fprintf(os.Stderr, "error: %s\n", d)
			}
			os.Exit(diagnostic.ExitError)
		}
		seedProg, spd := parser.Parse(seedFile, seedTokens)
		if spd.HasErrors() {
			for _, d := range spd {
				fmt.Fprintf(os.Stderr, "error: %s\n", d)
			}
			os.Exit(diagnostic.ExitError)
		}
		n, err := exec.Seed(ctx, seedProg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "talon run: seed: %v\n", err)
			os.Exit(diagnostic.ExitError)
		}
		fmt.Printf("==> seeded %d entity(s) from %s\n", n, seedFile)
	}

	// Execute all plans
	results, err := exec.RunAll(ctx, plans)
	if err != nil {
		fmt.Fprintf(os.Stderr, "talon run: %v\n", err)
		os.Exit(diagnostic.ExitError)
	}

	// Resolve entity names for readable output
	entityNames, resolveErr := exec.ResolveNames(ctx, nil)
	if resolveErr != nil {
		fmt.Fprintf(os.Stderr, "warn: could not resolve names: %v\n", resolveErr)
	}

	// Print results
	fmt.Printf("==> %s: %d block(s) executed\n", file, len(results))

	blockNames := make([]string, 0, len(results))
	for name := range results {
		blockNames = append(blockNames, name)
	}
	sort.Strings(blockNames)

	for _, name := range blockNames {
		r := results[name]
		fmt.Printf("\n  [%s] %d row(s) matched\n", r.BlockName, len(r.Flagged))
		for _, row := range r.Flagged {
			if len(row) == 0 {
				continue
			}
			eid, _ := row[0].(float64)
			ename := entityNames[int(eid)]
			if ename == "" {
				ename = fmt.Sprintf("entity %d", int(eid))
			}
			if len(row) == 1 {
				fmt.Printf("    - %s\n", ename)
			} else {
				// Include extra columns (e.g. km, last_service_km)
				extras := make([]string, 0, len(row)-1)
				for _, v := range row[1:] {
					extras = append(extras, fmt.Sprintf("%v", v))
				}
				fmt.Printf("    - %s (%s)\n", ename, strings.Join(extras, ", "))
			}
		}
	}
}

func runTrace() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: talon trace <rules.talon> <tests.talon.test> [--test NAME]")
		os.Exit(diagnostic.ExitUsage)
	}

	rulesPath := os.Args[2]
	testPath := os.Args[3]
	wantTest := ""
	for i := 4; i < len(os.Args); i++ {
		if os.Args[i] == "--test" && i+1 < len(os.Args) {
			wantTest = os.Args[i+1]
			i++
		}
	}

	rulesSrc, err := os.ReadFile(rulesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "talon trace: %v\n", err)
		os.Exit(diagnostic.ExitError)
	}
	rulesFile := filepath.Base(rulesPath)
	plans, ok := compile(rulesFile, string(rulesSrc))
	if !ok {
		os.Exit(diagnostic.ExitError)
	}

	testSrc, err := os.ReadFile(testPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "talon trace: %v\n", err)
		os.Exit(diagnostic.ExitError)
	}
	testFile := filepath.Base(testPath)
	testTokens, tld := lexer.Lex(testFile, string(testSrc))
	if tld.HasErrors() {
		for _, d := range tld {
			fmt.Fprintf(os.Stderr, "error: %s\n", d)
		}
		os.Exit(diagnostic.ExitError)
	}
	testProg, tpd := parser.Parse(testFile, testTokens)
	if tpd.HasErrors() {
		for _, d := range tpd {
			fmt.Fprintf(os.Stderr, "error: %s\n", d)
		}
		os.Exit(diagnostic.ExitError)
	}
	if tvd := testrunner.Validate(testProg, plans); tvd.HasErrors() {
		for _, d := range tvd {
			fmt.Fprintf(os.Stderr, "error: %s\n", d)
		}
		os.Exit(diagnostic.ExitError)
	}

	traces := testrunner.Trace(testProg, plans)
	if wantTest != "" {
		filtered := traces[:0]
		for _, t := range traces {
			if t.Name == wantTest {
				filtered = append(filtered, t)
			}
		}
		traces = filtered
		if len(traces) == 0 {
			fmt.Fprintf(os.Stderr, "talon trace: no test matched %q\n", wantTest)
			os.Exit(diagnostic.ExitError)
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{"traces": traces}); err != nil {
		fmt.Fprintf(os.Stderr, "talon trace: %v\n", err)
		os.Exit(diagnostic.ExitError)
	}
}

// compile runs lex → parse → validate → plan and returns plans.
func compile(file, src string) (map[string]*planner.QueryPlan, bool) {
	var allDiags diagnostic.List

	tokens, ld := lexer.Lex(file, src)
	allDiags = append(allDiags, ld...)

	prog, pd := parser.Parse(file, tokens)
	allDiags = append(allDiags, pd...)

	vd := validator.Validate(file, prog)
	allDiags = append(allDiags, vd...)

	for _, d := range allDiags {
		if d.Severity == diagnostic.Error {
			fmt.Fprintf(os.Stderr, "error: %s\n", d)
		} else if d.Severity == diagnostic.Warning {
			fmt.Fprintf(os.Stderr, "warn:  %s\n", d)
		}
	}
	if allDiags.HasErrors() {
		return nil, false
	}

	plans, planDiags := planner.Plan(prog)
	for _, d := range planDiags {
		fmt.Fprintf(os.Stderr, "error: %s\n", d)
	}
	if planDiags.HasErrors() {
		return nil, false
	}
	return plans, true
}

func printStep(num int, step planner.PlanStep) {
	switch s := step.(type) {
	case *planner.DatalevinQuery:
		fmt.Printf("    step %d  DatalevinQuery → %s\n", num, s.Into)
		// Indent the query
		for _, line := range strings.Split(s.Query, "\n") {
			fmt.Printf("            %s\n", line)
		}
	case *planner.GoComputation:
		if s.Input != "" {
			fmt.Printf("    step %d  GoComputation  %s(%s) → %s\n", num, s.Function, s.Input, s.Into)
		} else {
			fmt.Printf("    step %d  GoComputation  %s() → %s\n", num, s.Function, s.Into)
		}
	case *planner.Filter:
		fmt.Printf("    step %d  Filter         %s → %s\n", num, s.Input, s.Into)
	}
}

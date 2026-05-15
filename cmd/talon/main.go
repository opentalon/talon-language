package main

import (
	"context"
	"encoding/json"
	"fmt"
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
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: talon test <rules.talon> <tests.talon.test>")
		os.Exit(diagnostic.ExitUsage)
	}

	rulesPath := os.Args[2]
	testPath := os.Args[3]

	// Read and compile rules
	rulesSrc, err := os.ReadFile(rulesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "talon test: %v\n", err)
		os.Exit(diagnostic.ExitError)
	}

	rulesFile := filepath.Base(rulesPath)
	tokens, ld := lexer.Lex(rulesFile, string(rulesSrc))
	if ld.HasErrors() {
		for _, d := range ld {
			fmt.Fprintf(os.Stderr, "error: %s\n", d)
		}
		os.Exit(diagnostic.ExitError)
	}

	rulesProg, pd := parser.Parse(rulesFile, tokens)
	if pd.HasErrors() {
		for _, d := range pd {
			fmt.Fprintf(os.Stderr, "error: %s\n", d)
		}
		os.Exit(diagnostic.ExitError)
	}

	vd := validator.Validate(rulesFile, rulesProg)
	if vd.HasErrors() {
		for _, d := range vd {
			fmt.Fprintf(os.Stderr, "error: %s\n", d)
		}
		os.Exit(diagnostic.ExitError)
	}

	plans, planDiags := planner.Plan(rulesProg)
	if planDiags.HasErrors() {
		for _, d := range planDiags {
			fmt.Fprintf(os.Stderr, "error: %s\n", d)
		}
		os.Exit(diagnostic.ExitError)
	}

	// Read and parse test file
	testSrc, err := os.ReadFile(testPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "talon test: %v\n", err)
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

	// Validate test references
	tvd := testrunner.Validate(testProg, plans)
	if tvd.HasErrors() {
		for _, d := range tvd {
			fmt.Fprintf(os.Stderr, "error: %s\n", d)
		}
		os.Exit(diagnostic.ExitError)
	}

	// Run tests
	results := testrunner.Run(testProg, plans)
	fmt.Printf("==> %s: %d test(s)\n\n", testFile, len(results))

	passed, failed := testrunner.PrintResults(results)
	fmt.Printf("\n%d passed, %d failed\n", passed, failed)

	if failed > 0 {
		os.Exit(diagnostic.ExitError)
	}
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

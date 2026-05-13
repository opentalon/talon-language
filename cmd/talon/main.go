package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opentalon/talon-language/internal/diagnostic"
	"github.com/opentalon/talon-language/internal/lexer"
	"github.com/opentalon/talon-language/internal/parser"
	"github.com/opentalon/talon-language/internal/planner"
	"github.com/opentalon/talon-language/internal/testrunner"
	"github.com/opentalon/talon-language/internal/validator"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: talon <command> [args]")
		fmt.Fprintln(os.Stderr, "commands: build, test, repl, trace, mod")
		os.Exit(diagnostic.ExitUsage)
	}

	switch os.Args[1] {
	case "build":
		runBuild()
	case "test":
		runTest()
	case "repl":
		fmt.Fprintln(os.Stderr, "talon repl: not yet implemented")
		os.Exit(diagnostic.ExitError)
	case "trace":
		fmt.Fprintln(os.Stderr, "talon trace: not yet implemented")
		os.Exit(diagnostic.ExitError)
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

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
		fmt.Fprintln(os.Stderr, "talon test: not yet implemented")
		os.Exit(diagnostic.ExitError)
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

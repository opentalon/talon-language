package main

import (
	"fmt"
	"os"

	"github.com/opentalon/talon-language/internal/diagnostic"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: talon <command> [args]")
		fmt.Fprintln(os.Stderr, "commands: build, test, repl, trace, mod")
		os.Exit(diagnostic.ExitUsage)
	}

	switch os.Args[1] {
	case "build":
		fmt.Fprintln(os.Stderr, "talon build: not yet implemented")
		os.Exit(diagnostic.ExitError)
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

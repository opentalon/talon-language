package repl

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/diagnostic"
)

// Run drives the REPL: reads lines from in, evaluates them, writes output
// to out. It returns nil on a clean :quit / EOF, or an error if the I/O
// layer itself fails.
//
// The loop is single-threaded and synchronous. No history, no tab
// completion — those belong in a later pass behind a build tag or an
// external readline library.
func Run(in io.Reader, out io.Writer) error {
	return RunWithVersion(in, out, "")
}

// RunWithVersion is the same as Run but prints a version banner. The CLI
// passes its build-time version string; tests pass an empty string to
// suppress the banner for stable golden output.
func RunWithVersion(in io.Reader, out io.Writer, version string) error {
	if version != "" {
		fmt.Fprintf(out, "talon %s — type :help for commands, :quit to exit\n", version)
	}

	scanner := bufio.NewScanner(in)
	// Inputs can be much longer than the default 64 KiB when users paste
	// large block definitions. 1 MiB is plenty without being abusive.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	s := NewSession()
	var buf strings.Builder
	prompt(out, false)

	for scanner.Scan() {
		line := scanner.Text()

		// Slash commands only make sense as a fresh, single-line input.
		// If the user is mid-block, treat the leading colon as a literal
		// character of the block source (the parser will diagnose it).
		if buf.Len() == 0 && strings.HasPrefix(strings.TrimLeft(line, " \t"), ":") {
			if done := runCommand(s, strings.TrimLeft(line, " \t"), out); done {
				return nil
			}
			prompt(out, false)
			continue
		}

		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(line)

		if braceBalance(buf.String()) > 0 {
			prompt(out, true)
			continue
		}

		// Buffer is brace-balanced (or never had braces) — try to handle it.
		input := buf.String()
		buf.Reset()
		handleInput(s, input, out)
		prompt(out, false)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	// EOF is a normal exit.
	fmt.Fprintln(out)
	return nil
}

func prompt(w io.Writer, continuation bool) {
	if continuation {
		fmt.Fprint(w, "  .. ")
	} else {
		fmt.Fprint(w, "talon> ")
	}
}

// handleInput routes a complete (brace-balanced) buffer to the right
// handler. The dispatcher is in input.go.
func handleInput(s *Session, input string, w io.Writer) {
	switch classify(input) {
	case inputEmpty:
		return
	case inputCommand:
		// Slash commands inside a multi-line buffer aren't supported by the
		// main loop, but the dispatcher returns the same kind here for
		// completeness — handle gracefully.
		runCommand(s, strings.TrimSpace(input), w)
	case inputFactRecord:
		datum, err := parseRecordAssertion(strings.TrimSpace(input))
		if err != nil {
			fmt.Fprintf(w, "  fact error: %v\n", err)
			return
		}
		s.AddFact(datum)
		fmt.Fprintf(w, "  OK: record %d\n", datum.ID)
	case inputFactAttr:
		datum, err := parseAttrAssertion(strings.TrimSpace(input))
		if err != nil {
			fmt.Fprintf(w, "  fact error: %v\n", err)
			return
		}
		s.AddFact(datum)
		fmt.Fprintf(w, "  OK: attr %d\n", datum.ID)
	case inputBlock:
		handleBlockInput(s, input, w)
	}
}

func handleBlockInput(s *Session, src string, w io.Writer) {
	prog, diags := parseSource(src)
	printDiagnostics(diags, w)
	if diags.HasErrors() {
		return
	}
	if len(prog.Blocks) == 0 {
		// Empty input post-comments etc.
		return
	}

	// Validate the merged program. We do this before committing the new
	// blocks so a bad rename / typo doesn't silently overwrite a working
	// session block.
	merged := &ast.Program{Blocks: append(append([]ast.Block(nil), s.Blocks...), prog.Blocks...)}
	// dedupe-by-name for validation (later blocks win, matching AddBlocks).
	merged.Blocks = dedupeByName(merged.Blocks)
	if _, vdiags, err := compileProgram(merged); err != nil {
		printDiagnostics(vdiags, w)
		fmt.Fprintln(w, "  blocks not added")
		return
	} else {
		printDiagnostics(vdiags, w)
	}

	s.AddBlocks(prog.Blocks)
	names := make([]string, 0, len(prog.Blocks))
	for _, b := range prog.Blocks {
		names = append(names, fmt.Sprintf("%s %q", blockKind(b), b.BlockName()))
	}
	fmt.Fprintf(w, "  OK: %s\n", strings.Join(names, ", "))
}

func dedupeByName(blocks []ast.Block) []ast.Block {
	seen := map[string]int{}
	out := make([]ast.Block, 0, len(blocks))
	for _, b := range blocks {
		if idx, ok := seen[b.BlockName()]; ok {
			out[idx] = b
			continue
		}
		seen[b.BlockName()] = len(out)
		out = append(out, b)
	}
	return out
}

func printDiagnostics(diags diagnostic.List, w io.Writer) {
	for _, d := range diags {
		switch d.Severity {
		case diagnostic.Error:
			fmt.Fprintf(w, "  error: %s\n", d)
		case diagnostic.Warning:
			fmt.Fprintf(w, "  warn:  %s\n", d)
		}
	}
}

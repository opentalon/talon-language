package repl

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/pkg/talon"
)

// echoCaller is the REPL's stand-in for an MCP server. A real deployment
// routes mcp steps to opentalon-mcp; in the REPL we print the call so the
// effect of a fired workflow is visible, and return a stub result so the
// step succeeds.
type echoCaller struct{ w io.Writer }

func (c echoCaller) Call(_ context.Context, server, tool string, args map[string]any) (any, error) {
	fmt.Fprintf(c.w, "  ↻ mcp %s.%s %s\n", server, tool, formatArgs(args))
	return map[string]any{"result": map[string]any{"status": "ok"}}, nil
}

func formatArgs(args map[string]any) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, args[k]))
	}
	return "{" + strings.Join(parts, " ") + "}"
}

// startWatch builds a reactive session from src, replacing any previous
// one. mcp steps in fired workflows are routed to an echoCaller writing
// to w. A program with no on-blocks is still valid — it simply never
// fires.
func (s *Session) startWatch(src string, w io.Writer) error {
	sess, err := talon.NewSession(src, talon.WithMCP(echoCaller{w: w}))
	if err != nil {
		return err
	}
	if s.watch != nil {
		s.watch.Close()
	}
	s.watch = sess
	return nil
}

// stopWatch tears down the reactive session, if any.
func (s *Session) stopWatch() {
	if s.watch != nil {
		s.watch.Close()
		s.watch = nil
	}
}

// fireWatch asserts a fact datum into the reactive session and prints any
// firings. It is a no-op when watch mode is inactive, so the legacy
// (non-reactive) REPL flow is unaffected.
func (s *Session) fireWatch(datum ast.TestDatum, w io.Writer) {
	if s.watch == nil {
		return
	}
	firings, err := s.watch.Assert(context.Background(), factsFromDatum(datum))
	if err != nil {
		fmt.Fprintf(w, "  watch error: %v\n", err)
		return
	}
	printFirings(firings, w)
}

// factsFromDatum converts a REPL record/attr assertion into the EAV facts
// the reactive session reasons over — one fact per field.
func factsFromDatum(d ast.TestDatum) []talon.Fact {
	id := strconv.Itoa(d.ID)
	keys := make([]string, 0, len(d.Fields))
	for k := range d.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	facts := make([]talon.Fact, 0, len(keys))
	for _, k := range keys {
		facts = append(facts, talon.Fact{RecordID: id, Attribute: k, Value: d.Fields[k]})
	}
	return facts
}

func printFirings(firings []talon.Firing, w io.Writer) {
	for _, f := range firings {
		switch {
		case f.Err != nil:
			fmt.Fprintf(w, "  ✗ [%s] %s %q: %v\n", f.OnBlock, f.RefKind, f.Ref, f.Err)
		case f.Ref == "":
			fmt.Fprintf(w, "  • [%s] matched\n", f.OnBlock)
		default:
			fmt.Fprintf(w, "  ✓ [%s] fired %s %q\n", f.OnBlock, f.RefKind, f.Ref)
		}
	}
}

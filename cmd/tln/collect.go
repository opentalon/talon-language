package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/diagnostic"
	"github.com/opentalon/tln-language/internal/executor"
	"github.com/opentalon/tln-language/internal/factstore"
	"github.com/opentalon/tln-language/internal/lexer"
	"github.com/opentalon/tln-language/internal/parser"
)

// runCollect dispatches `tln collect <list|run> ...`. tln does not run
// a scheduler: `list` emits schedule metadata for a host cron to consume,
// and `run` fires one execution when the host decides to.
func runCollect() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: tln collect <list|run> <file.tln> [--name NAME]")
		os.Exit(diagnostic.ExitUsage)
	}
	switch os.Args[2] {
	case "list":
		runCollectList()
	case "run":
		runCollectRun()
	default:
		fmt.Fprintf(os.Stderr, "tln collect: unknown subcommand %q (want list or run)\n", os.Args[2])
		os.Exit(diagnostic.ExitUsage)
	}
}

type collectInfo struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Server   string `json:"server"`
	Tool     string `json:"tool"`
	StoreAs  string `json:"store_as"`
	Tag      string `json:"tag,omitempty"`
}

// runCollectList prints every collect block's schedule metadata as JSON —
// the interface a host scheduler (OpenTalon, k8s CronJobs) reads to
// populate its own job queue.
func runCollectList() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: tln collect list <file.tln>")
		os.Exit(diagnostic.ExitUsage)
	}
	blocks := parseCollectBlocks(os.Args[3])
	out := make([]collectInfo, 0, len(blocks))
	for _, b := range blocks {
		info := collectInfo{Name: b.Name, Schedule: b.Schedule, StoreAs: b.StoreAs, Tag: b.Tag}
		if b.Call != nil {
			info.Server, info.Tool = b.Call.Server, b.Call.Tool
		}
		out = append(out, info)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "tln collect list: %v\n", err)
		os.Exit(diagnostic.ExitError)
	}
}

// runCollectRun fires one execution of the named collect block: fetch from
// the MCP tool and assert the results into the FactStore. The standalone
// CLI has no MCP transport, so a host normally drives collection through
// the SDK; here `run` wires the store and reports what was asserted.
func runCollectRun() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: tln collect run <file.tln> --name NAME")
		os.Exit(diagnostic.ExitUsage)
	}
	path := os.Args[3]
	name := ""
	for i := 4; i < len(os.Args); i++ {
		if os.Args[i] == "--name" && i+1 < len(os.Args) {
			name = os.Args[i+1]
			i++
		}
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "tln collect run: --name is required")
		os.Exit(diagnostic.ExitUsage)
	}

	var target *ast.CollectBlock
	for _, b := range parseCollectBlocks(path) {
		if b.Name == name {
			target = b
			break
		}
	}
	if target == nil {
		fmt.Fprintf(os.Stderr, "tln collect run: no collect block named %q in %s\n", name, path)
		os.Exit(diagnostic.ExitError)
	}

	ctx := context.Background()
	store := factstore.NewMemoryStore()
	exec := executor.NewExecutor(store)
	// exec.Tools stays nil: the standalone CLI has no MCP transport. A host
	// injects a caller via the SDK; here the fetch is a no-op.
	n, err := exec.RunCollect(ctx, target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tln collect run: %v\n", err)
		os.Exit(diagnostic.ExitError)
	}
	fmt.Printf("collected %d record(s) for %q\n", n, name)
	if exec.Tools == nil {
		fmt.Fprintln(os.Stderr, "note: standalone tln has no MCP transport; a host drives collection via the SDK (WithToolResolver).")
	}
}

// parseCollectBlocks lex/parses a file and returns its collect blocks,
// exiting with a diagnostic on parse errors.
func parseCollectBlocks(path string) []*ast.CollectBlock {
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tln collect: %v\n", err)
		os.Exit(diagnostic.ExitError)
	}
	label := path
	tokens, ld := lexer.Lex(label, string(src))
	prog, pd := parser.Parse(label, tokens)
	diags := append(append(diagnostic.List{}, ld...), pd...)
	for _, d := range diags {
		if d.Severity == diagnostic.Error {
			fmt.Fprintf(os.Stderr, "error: %s\n", d)
		}
	}
	if diags.HasErrors() {
		os.Exit(diagnostic.ExitError)
	}
	var out []*ast.CollectBlock
	for _, b := range prog.Blocks {
		if c, ok := b.(*ast.CollectBlock); ok {
			out = append(out, c)
		}
	}
	return out
}

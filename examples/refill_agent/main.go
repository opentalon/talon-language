// Command refill-agent is a runnable console demo of a Talon watcher
// agent built on the pkg/talon reactive Session API.
//
// It simulates two successive "fetches" from an inventory source,
// mapping each item's stock level into facts and asserting them into a
// Session. When an item's current_stock transitions to 0, the program's
// `on change` block fires a refill workflow, whose mcp step is routed to
// a console stand-in that prints the refill action.
//
// Run it:
//
//	go run ./examples/refill_agent
package main

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"sort"

	talonlog "github.com/opentalon/talon-language/internal/log"
	"github.com/opentalon/talon-language/pkg/talon"
)

//go:embed refill_agent.talon
var program string

// labels map integer record ids to human names for readable output.
// (The FactStore keys entities by integer id; a real agent keeps this
// mapping in its external-id registry.)
var labels = map[string]string{"1": "cement", "2": "sand", "3": "gravel"}

// consoleCaller stands in for the "inventory" MCP server. A real agent
// routes mcp steps to opentalon-mcp; here we just narrate the action and
// return a synthetic order id so the workflow step succeeds.
type consoleCaller struct{ orders int }

func (c *consoleCaller) Call(_ context.Context, server, tool string, args map[string]any) (any, error) {
	c.orders++
	order := fmt.Sprintf("PO-%04d", 1000+c.orders)
	item := fmt.Sprintf("%v", args["item_id"])
	fmt.Printf("    ↻ refilled %s — [%s.%s] qty=%v → order %s\n",
		label(item), server, tool, args["quantity"], order)
	return map[string]any{"result": map[string]any{"id": order}}, nil
}

func label(id string) string {
	if l, ok := labels[id]; ok {
		return l
	}
	return "item " + id
}

func main() {
	// Quiet the INFO-level plumbing so the on-block's warn line and our
	// own narration are what show up.
	talonlog.Init(talonlog.FormatText, slog.LevelWarn, os.Stderr)

	caller := &consoleCaller{}
	session, err := talon.NewSession(program, talon.WithMCP(caller))
	if err != nil {
		fmt.Println("compile error:", err)
		os.Exit(1)
	}
	defer session.Close()

	fmt.Println("=== refill watcher agent ===")
	fmt.Println(`program loaded; watching attr "current_stock" for transitions to 0`)

	// Two successive fetches from the (simulated) inventory source.
	// Tick 1 seeds the levels; tick 2 shows cement drained to 0.
	ticks := []map[string]int{
		{"1": 8, "2": 3},
		{"1": 0, "2": 3},
	}

	for i, dump := range ticks {
		fmt.Printf("\n[tick %d] fetch dump:\n", i+1)
		for _, id := range sortedKeys(dump) {
			fmt.Printf("  %-8s current_stock=%d\n", label(id), dump[id])
		}

		firings, err := session.Assert(context.Background(), factsFromDump(dump))
		if err != nil {
			fmt.Println("  assert error:", err)
			continue
		}
		if len(firings) == 0 {
			fmt.Println("  → no firings")
			continue
		}
		for _, f := range firings {
			fmt.Printf("  → [%s] fired %s %q\n", f.OnBlock, f.RefKind, f.Ref)
			if f.Err != nil {
				fmt.Println("     error:", f.Err)
			}
		}
	}

	fmt.Println("\ndone.")
}

// factsFromDump maps a fetch result (record id → stock level) into the
// EAV facts the Session reasons over: a type fact and a current_stock
// fact per item. This mirrors the poll → mapper → assert path a real
// agent uses.
func factsFromDump(dump map[string]int) []talon.Fact {
	facts := make([]talon.Fact, 0, len(dump)*2)
	for _, id := range sortedKeys(dump) {
		facts = append(facts,
			talon.Fact{RecordID: id, Attribute: "type", Value: "stock_item"},
			talon.Fact{RecordID: id, Attribute: "current_stock", Value: dump[id]},
		)
	}
	return facts
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

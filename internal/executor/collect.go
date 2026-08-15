package executor

import (
	"context"
	"sort"
	"strconv"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/factstore"
	"github.com/opentalon/tln-language/internal/template"
)

// RunCollect executes one collect block: dispatch its MCP call (with the
// block's on_error policy), map the response's items into facts of type
// StoreAs (tagged Tag), and assert them into the FactStore. Returns the
// number of records asserted. With no MCP caller wired it's a no-op
// (the standalone CLI has no MCP transport; a host injects one).
func (e *Executor) RunCollect(ctx context.Context, b *ast.CollectBlock) (int, error) {
	if b == nil || b.Call == nil || e.Tools == nil {
		return 0, nil
	}
	// collect args are literals (no per-row context); resolve against an
	// empty row so string templates / literals evaluate.
	row := map[string]any{}
	rctx := template.RenderContext{Row: row}
	args := make(map[string]any, len(b.Call.Args))
	for k, expr := range b.Call.Args {
		args[k] = resolveRemediateArg(expr, row, rctx)
	}

	resp, skipped, err := e.dispatchMCP(ctx, b.Call.Server, b.Call.Tool, args, b.Call.OnError, row)
	if err != nil || skipped {
		return 0, err
	}

	facts := factsFromCollect(resp, b.StoreAs, b.Tag)
	if len(facts) == 0 || e.Client == nil {
		return 0, nil
	}
	if err := e.Client.Assert(ctx, facts); err != nil {
		return 0, err
	}
	return countRecords(facts), nil
}

// factsFromCollect maps an MCP list response into EAV facts. Each item
// becomes a record of type typeName with a `tag` attribute (when set) and
// one :attr/<field> per top-level scalar. Record IDs come from the item's
// `id` field when integer-parseable, else a per-batch sequential index.
func factsFromCollect(resp any, typeName, tag string) []factstore.Fact {
	items := collectItems(resp)
	var facts []factstore.Fact
	for i, item := range items {
		rec := collectRecordID(item, i)
		facts = append(facts, factstore.Fact{RecordID: rec, Attribute: ":record/type", Value: typeName})
		if tag != "" {
			facts = append(facts, factstore.Fact{RecordID: rec, Attribute: ":attr/tag", Value: tag})
		}
		for _, k := range sortedItemKeys(item) {
			v := item[k]
			if !isScalar(v) {
				continue
			}
			facts = append(facts, factstore.Fact{RecordID: rec, Attribute: ":attr/" + k, Value: v})
		}
	}
	return facts
}

// collectItems extracts the list of items from an MCP response: a bare
// array, or the first of items/results/data holding an array, or the
// whole object treated as a single item.
func collectItems(resp any) []map[string]any {
	switch v := resp.(type) {
	case []any:
		return toItemMaps(v)
	case map[string]any:
		for _, key := range []string{"items", "results", "data"} {
			if arr, ok := v[key].([]any); ok {
				return toItemMaps(arr)
			}
		}
		return []map[string]any{v}
	}
	return nil
}

func toItemMaps(arr []any) []map[string]any {
	out := make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func collectRecordID(item map[string]any, idx int) string {
	if v, ok := item["id"]; ok {
		switch n := v.(type) {
		case float64:
			return strconv.Itoa(int(n))
		case int:
			return strconv.Itoa(n)
		case string:
			if _, err := strconv.Atoi(n); err == nil {
				return n
			}
		}
	}
	return strconv.Itoa(idx + 1)
}

func isScalar(v any) bool {
	switch v.(type) {
	case string, float64, float32, int, int64, bool:
		return true
	}
	return false
}

func sortedItemKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func countRecords(facts []factstore.Fact) int {
	seen := map[string]struct{}{}
	for _, f := range facts {
		seen[f.RecordID] = struct{}{}
	}
	return len(seen)
}

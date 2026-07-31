package executor

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/factstore"
	"github.com/opentalon/talon-language/internal/planner"
	"github.com/opentalon/talon-language/internal/template"
)

// execEnrich refreshes stale facts from an MCP tool. For each candidate
// record whose target attributes are older than the block's stale_after
// bound (per the FactStore's Freshness capability), it calls the MCP tool
// once with per-row args and asserts the mapped response fields back into
// the store. Records still within their freshness window are skipped, so
// re-running enrich doesn't re-fetch fresh data.
//
// With no MCP caller wired the step is a no-op. When the store doesn't
// implement Freshness, staleness can't be determined, so every candidate
// is treated as stale (refresh) — the safe default.
func (e *Executor) execEnrich(ctx context.Context, gc *planner.GoComputation, vars map[string]any) (any, error) {
	call, _ := gc.Params["call"].(*ast.MCPCall)
	updates, _ := gc.Params["updates"].([]ast.UpdateClause)
	staleAfter, _ := gc.Params["stale_after"].(ast.Duration)
	rows, _ := vars[gc.Input].([][]any)

	summary := map[string]any{"refreshed": 0, "candidates": len(rows)}
	if call == nil || len(rows) == 0 {
		return summary, nil
	}

	var ids []int
	seen := map[int]bool{}
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		id, ok := toEntityID(r[0])
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}

	fresh, hasFreshness := e.Client.(factstore.Freshness)
	cutoff := durationValue(staleAfter)
	now := time.Now()
	attrsByID := e.fetchEntityAttrs(ctx, ids, referencedAttrs([]ast.Action{&ast.MCPAction{Call: call}}))

	refreshed := 0
	for _, id := range ids {
		if hasFreshness && !recordStale(fresh, id, updates, now, cutoff) {
			continue // still fresh — skip the fetch
		}

		row := attrsByID[id]
		if row == nil {
			row = map[string]any{}
		}
		row["id"] = id
		rctx := template.RenderContext{Row: row}

		if e.MCP == nil {
			continue // no caller: stub
		}
		args := make(map[string]any, len(call.Args))
		for k, expr := range call.Args {
			args[k] = resolveRemediateArg(expr, row, rctx)
		}
		resp, skipped, err := e.dispatchMCP(ctx, call.Server, call.Tool, args, call.OnError, row)
		if err != nil || skipped {
			continue
		}

		facts := factsFromResponse(id, resp, updates)
		if len(facts) == 0 {
			continue
		}
		if err := e.Client.Assert(ctx, facts); err != nil {
			return summary, err
		}
		refreshed++
	}
	summary["refreshed"] = refreshed
	return summary, nil
}

// recordStale reports whether any of the record's update-target
// attributes is older than cutoff (or has no recorded write-time).
func recordStale(fresh factstore.Freshness, id int, updates []ast.UpdateClause, now time.Time, cutoff time.Duration) bool {
	rec := strconv.Itoa(id)
	for _, u := range updates {
		t, ok := fresh.LastWritten(rec, ":attr/"+u.Attr)
		if !ok || now.Sub(t) > cutoff {
			return true
		}
	}
	return false
}

// factsFromResponse maps each update clause's result path against the MCP
// response into a fact on the record's :attr/<name> cell.
func factsFromResponse(id int, resp any, updates []ast.UpdateClause) []factstore.Fact {
	m, ok := resp.(map[string]any)
	if !ok {
		return nil
	}
	rec := strconv.Itoa(id)
	var facts []factstore.Fact
	for _, u := range updates {
		val := navigatePath(m, u.ResultPath)
		if val == nil {
			continue
		}
		facts = append(facts, factstore.Fact{
			RecordID:  rec,
			Attribute: ":attr/" + u.Attr,
			Value:     val,
		})
	}
	return facts
}

// navigatePath walks a dot-separated path into a decoded JSON object.
func navigatePath(m map[string]any, path string) any {
	var cur any = m
	for _, seg := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[seg]
	}
	return cur
}

// durationValue converts an ast.Duration to a time.Duration, accepting
// both singular and plural units. Months/years use calendar-average
// approximations (30 / 365 days) — fine for freshness windows.
func durationValue(d ast.Duration) time.Duration {
	unit := strings.TrimSuffix(strings.ToLower(d.Unit), "s")
	n := time.Duration(d.Value)
	switch unit {
	case "second":
		return n * time.Second
	case "minute":
		return n * time.Minute
	case "hour":
		return n * time.Hour
	case "day":
		return n * 24 * time.Hour
	case "week":
		return n * 7 * 24 * time.Hour
	case "month":
		return n * 30 * 24 * time.Hour
	case "year":
		return n * 365 * 24 * time.Hour
	}
	return 0
}

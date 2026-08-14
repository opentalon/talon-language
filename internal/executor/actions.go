package executor

import (
	"context"
	"sort"

	"github.com/opentalon/tln-language/internal/actions"
	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/factstore"
	"github.com/opentalon/tln-language/internal/planner"
	"github.com/opentalon/tln-language/internal/template"
)

// FiredAction is one `do` action resolved against one matched row. tln
// does not execute it — the host does. This is the data the engine hands
// back on the block result.
type FiredAction = actions.Fired

// execFireActions resolves a rule's `do` clauses against the rows it
// matched. Actions fire in source order per row, rows in flagged-set
// order. Nothing is dispatched: the result is a payload for the host.
func (e *Executor) execFireActions(ctx context.Context, gc *planner.GoComputation, vars map[string]any) []FiredAction {
	rule, _ := gc.Params["rule"].(*ast.RuleBlock)
	if rule == nil || len(rule.Do) == 0 {
		return nil
	}
	rows, _ := vars[gc.Input].([][]any)

	// Distinct entity ids, preserving flagged-row order.
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
	if len(ids) == 0 {
		return nil
	}

	attrsByID, flatByID := e.fetchActionRows(ctx, ids, actions.ReferencedAttrs(rule))

	// AggregateRows carries every matched row so a literal argument can
	// interpolate {count} / {avg(attr.x)} the same way a label does.
	agg := make([]template.Row, 0, len(ids))
	for _, id := range ids {
		agg = append(agg, template.Row(flatByID[id]))
	}

	now := e.now()
	actionRows := make([]actions.Row, 0, len(ids))
	for _, id := range ids {
		attrs := attrsByID[id]
		if attrs == nil {
			attrs = map[string]any{}
		}
		actionRows = append(actionRows, actions.Row{
			ID:    id,
			Attrs: attrs,
			Ctx: template.RenderContext{
				Row:           template.Row(flatByID[id]),
				AggregateRows: agg,
				Now:           now,
			},
		})
	}
	return actions.Fire(rule, actionRows)
}

// fetchActionRows returns two views of each entity: attrs holds only the
// :attr/ namespace, which is what `attr "x"` reads (an attribute the row
// does not carry stays absent, so it resolves to nil rather than to an
// empty string); flat additionally folds in :record/ fields under bare
// names, which is the view templates render against.
func (e *Executor) fetchActionRows(ctx context.Context, ids []int, names []string) (attrs, flat map[int]map[string]any) {
	attrs = map[int]map[string]any{}
	flat = map[int]map[string]any{}
	for _, id := range ids {
		attrs[id] = map[string]any{}
		flat[id] = map[string]any{"id": id}
	}
	if e.Client == nil || len(names) == 0 {
		return attrs, flat
	}
	want := map[int]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, name := range names {
		// :record/ first, then :attr/ over it — an attribute asserted in
		// both namespaces resolves to the attr value, matching the
		// flattening the test runner and label rendering use.
		for _, ns := range []string{":record/", ":attr/"} {
			rows, err := e.Client.Query(ctx, factstore.Query{
				Find: []string{"?e", "?v"},
				Where: []factstore.Clause{&factstore.Pattern{
					Entity:    factstore.Var("?e"),
					Attribute: ns + name,
					Value:     factstore.Var("?v"),
				}},
			})
			if err != nil {
				continue
			}
			for _, r := range rows {
				if len(r) < 2 {
					continue
				}
				id, ok := toEntityID(r[0])
				if !ok || !want[id] {
					continue
				}
				flat[id][name] = r[1]
				if ns == ":attr/" {
					attrs[id][name] = r[1]
				}
			}
		}
	}
	return attrs, flat
}

// resolveDefeatedActions drops the actions of rules that lost defeasible
// resolution against another rule matching the same row. Resolution runs
// across every rule plan in the run — a `strict` rule in one block can
// defeat an `approve` in another — so it happens here, after all blocks
// have executed, rather than inside a single block's step list.
func resolveDefeatedActions(plans map[string]*planner.QueryPlan, results map[string]*BlockResult) []string {
	matched := map[int][]*ast.RuleBlock{}
	for name, plan := range plans {
		if plan.Rule == nil {
			continue
		}
		res, ok := results[name]
		if !ok {
			continue
		}
		for _, id := range rowEntityIDs(res.Flagged) {
			matched[id] = append(matched[id], plan.Rule)
		}
	}
	var warnings []string
	seen := map[string]bool{}
	for _, res := range results {
		if len(res.Actions) == 0 {
			continue
		}
		kept, warns := actions.Resolve(res.Actions, matched)
		res.Actions = kept
		for _, w := range warns {
			if seen[w] {
				continue
			}
			seen[w] = true
			warnings = append(warnings, w)
		}
	}
	// results is a map; sort so the same run reports the same list.
	sort.Strings(warnings)
	return warnings
}

// rowEntityIDs reads the distinct entity ids out of a flagged row set, in
// row order.
func rowEntityIDs(rows [][]any) []int {
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
	return ids
}

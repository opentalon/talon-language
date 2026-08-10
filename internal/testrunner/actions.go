package testrunner

import (
	"fmt"
	"strings"
	"time"

	"github.com/opentalon/talon-language/internal/actions"
	"github.com/opentalon/talon-language/internal/ast"
)

// FiredAction is one `do` action resolved against one matched row. Talon does
// not execute it — the host does. This is the data the engine hands back.
type FiredAction = actions.Fired

// FireBlockActions resolves the `do` clauses of a rule for every flagged row,
// in source order, row order following the flagged set. A block with no `do`
// clauses returns nothing.
//
// Firing itself lives in internal/actions, shared with the runtime, so a
// `did` / `did_not` assertion is checked against the same resolution a host
// embedding the engine receives. All this does is project the test runner's
// in-memory entities into the rows that package wants.
func FireBlockActions(b ast.Block, flagged []int, entities map[int]*entity, now time.Time) []FiredAction {
	rule, ok := b.(*ast.RuleBlock)
	if !ok {
		return nil
	}
	rows := make([]actions.Row, 0, len(flagged))
	for _, id := range flagged {
		ent, ok := entities[id]
		if !ok || ent == nil {
			continue
		}
		rows = append(rows, actions.Row{
			ID:    id,
			Attrs: attrFields(ent),
			Ctx:   renderContextFor(ent, flagged, entities, now, nil, ""),
		})
	}
	return actions.Fire(rule, rows)
}

// attrFields projects an entity's ":attr/x" fields to bare names — the view
// `attr "x"` arguments read. Record fields stay out, so `attr` naming a fact
// the row does not carry resolves to nil rather than to a record value.
func attrFields(ent *entity) map[string]any {
	out := make(map[string]any, len(ent.fields))
	for k, v := range ent.fields {
		if name, ok := strings.CutPrefix(k, ":attr/"); ok {
			out[name] = v
		}
	}
	return out
}

// checkActionAssertions verifies the did / did_not assertions in an expect
// block against the actions the rule actually fired.
func checkActionAssertions(asserts []ast.ActionAssertion, fired []FiredAction) []string {
	var errs []string
	for _, a := range asserts {
		matched := false
		for _, f := range fired {
			if f.EntityID == a.ID && f.Verb == a.Verb && actionArgsMatch(a.Args, f.Args) {
				matched = true
				break
			}
		}
		switch {
		case a.Negate && matched:
			errs = append(errs, fmt.Sprintf(
				"expected entity %d NOT to %s, but it did", a.ID, describeAssertion(a)))
		case !a.Negate && !matched:
			errs = append(errs, fmt.Sprintf(
				"expected entity %d to %s, but it did not — fired: %s",
				a.ID, describeAssertion(a), describeFired(a.ID, fired)))
		}
	}
	return errs
}

// actionArgsMatch compares an assertion's positional matchers against a fired
// action's arguments. Fewer matchers than arguments is a prefix match, so
// `did 1 comment "pr"` holds regardless of the comment body; more matchers
// than arguments never matches.
func actionArgsMatch(want []ast.ActionArgMatch, got []any) bool {
	if len(want) > len(got) {
		return false
	}
	for i, w := range want {
		if w.Contains {
			needle, ok := w.Value.(string)
			if !ok {
				return false
			}
			hay, ok := got[i].(string)
			if !ok || !strings.Contains(hay, needle) {
				return false
			}
			continue
		}
		if !sameActionValue(w.Value, got[i]) {
			return false
		}
	}
	return true
}

// sameActionValue compares an asserted literal with a resolved argument.
// Numbers are compared as float64 since the test parser and the fact store
// disagree about int vs float for the same source text.
func sameActionValue(want, got any) bool {
	if wf, ok := numericValue(want); ok {
		if gf, ok := numericValue(got); ok {
			return wf == gf
		}
		return false
	}
	return want == got
}

func describeAssertion(a ast.ActionAssertion) string {
	var b strings.Builder
	b.WriteString(a.Verb)
	for _, arg := range a.Args {
		if arg.Contains {
			fmt.Fprintf(&b, " contains %q", arg.Value)
			continue
		}
		fmt.Fprintf(&b, " %v", formatActionArg(arg.Value))
	}
	return b.String()
}

// describeFired renders what the row actually did, so a failure names the gap
// rather than only the expectation.
func describeFired(id int, fired []FiredAction) string {
	var parts []string
	for _, f := range fired {
		if f.EntityID != id {
			continue
		}
		var b strings.Builder
		b.WriteString(f.Verb)
		for _, arg := range f.Args {
			fmt.Fprintf(&b, " %v", formatActionArg(arg))
		}
		parts = append(parts, b.String())
	}
	if len(parts) == 0 {
		return "(nothing)"
	}
	return strings.Join(parts, "; ")
}

func formatActionArg(v any) string {
	if s, ok := v.(string); ok {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf("%v", v)
}

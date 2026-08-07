package testrunner

import (
	"fmt"
	"strings"
	"time"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/template"
)

// FiredAction is one `do` action resolved against one matched row. Talon does
// not execute it — the host does. This is the data the engine hands back.
type FiredAction struct {
	EntityID int
	Verb     string
	Args     []any
}

// FireBlockActions resolves the `do` clauses of a rule for every flagged row,
// in source order, row order following the flagged set. A block with no `do`
// clauses returns nothing.
//
// Arguments resolve per row: `attr "x"` reads the row's attribute, a string
// literal is rendered as a template so `{attr.x}` interpolates, and numbers and
// booleans pass through. An `attr` that the row does not carry resolves to nil,
// which keeps a missing fact distinguishable from an empty string in the
// action payload even though the condition layer is two-valued.
func FireBlockActions(b ast.Block, flagged []int, entities map[int]*entity, now time.Time) []FiredAction {
	rule, ok := b.(*ast.RuleBlock)
	if !ok || len(rule.Do) == 0 {
		return nil
	}
	var out []FiredAction
	for _, id := range flagged {
		ent, ok := entities[id]
		if !ok || ent == nil {
			continue
		}
		ctx := renderContextFor(ent, flagged, entities, now, nil, "")
		for _, do := range rule.Do {
			fired := FiredAction{EntityID: id, Verb: do.Verb}
			for _, arg := range do.Args {
				fired.Args = append(fired.Args, resolveActionArg(arg, ent, ctx))
			}
			out = append(out, fired)
		}
	}
	return out
}

// resolveActionArg evaluates one `do` argument against a row.
func resolveActionArg(e ast.Expr, ent *entity, ctx template.RenderContext) any {
	switch v := e.(type) {
	case *ast.AttrExpr:
		if val, ok := ent.fields[":attr/"+v.Name]; ok {
			return val
		}
		return nil
	case *ast.LiteralExpr:
		if s, ok := v.Value.(string); ok {
			return template.Render(ast.ParseTemplate(s), ctx)
		}
		return v.Value
	case *ast.IdentExpr:
		return v.Name
	}
	return nil
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
	if wf, ok := actionNumeric(want); ok {
		if gf, ok := actionNumeric(got); ok {
			return wf == gf
		}
		return false
	}
	return want == got
}

func actionNumeric(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
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

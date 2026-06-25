package talondb

import (
	"strings"

	"github.com/opentalon/talon-language/internal/factstore"
)

// matchAll returns true when every clause in `clauses` matches the
// document `attrs` under the current bindings. New variables bound by
// any clause are added to bindings.
func matchAll(clauses []factstore.Clause, attrs map[string]any, bindings map[string]any) bool {
	for _, c := range clauses {
		if !matchOne(c, attrs, bindings) {
			return false
		}
	}
	return true
}

// matchOne dispatches on clause type. Pattern, Predicate, Or, Not,
// and FullText are supported; unknown clauses fail closed.
func matchOne(c factstore.Clause, attrs map[string]any, bindings map[string]any) bool {
	switch cc := c.(type) {
	case *factstore.Pattern:
		return matchPattern(cc, attrs, bindings)
	case *factstore.Predicate:
		return matchPredicate(cc, bindings)
	case *factstore.Or:
		return matchOr(cc, attrs, bindings)
	case *factstore.Not:
		return matchNot(cc, attrs, bindings)
	case *factstore.FullText:
		return matchFullText(cc, attrs)
	}
	return false
}

// matchPattern verifies / binds a Pattern against the in-memory doc.
//
//   - Literal attribute + literal value: check attrs[attr] equals value
//     (anchors already narrowed, so this is a safety net).
//   - Literal attribute + variable value: bind the variable to
//     attrs[attr]; if the variable is already bound, verify equality.
//   - Variable attribute or wildcard attribute: not supported (planner
//     does not emit; would require iterating attrs).
func matchPattern(p *factstore.Pattern, attrs map[string]any, bindings map[string]any) bool {
	if p.Attribute == "" {
		return false
	}
	docVal, present := attrs[p.Attribute]
	if !present {
		return false
	}
	switch {
	case p.Value.Literal != nil:
		return equalAny(docVal, p.Value.Literal)
	case p.Value.Var != "":
		if existing, had := bindings[p.Value.Var]; had {
			return equalAny(existing, docVal)
		}
		bindings[p.Value.Var] = docVal
		return true
	default:
		// Wildcard value: doc just needs to have the attribute.
		return true
	}
}

// matchPredicate resolves both terms against bindings and applies the
// operator. Bindings flow in only; predicates produce no new bindings.
func matchPredicate(p *factstore.Predicate, bindings map[string]any) bool {
	left := resolveTerm(p.Left, bindings)
	right := resolveTerm(p.Right, bindings)
	return evalPredicate(p.Op, left, right)
}

// matchOr returns true when any branch matches. Per Datalog semantics,
// bindings made inside an Or branch must NOT leak to siblings — but
// they SHOULD leak to the enclosing query for variables that were
// previously unbound and that all surviving branches happen to bind
// to the same value. We follow MemoryStore's simpler rule: on the
// first successful branch, copy new bindings into the parent.
func matchOr(o *factstore.Or, attrs map[string]any, bindings map[string]any) bool {
	for _, branch := range o.Branches {
		scratch := cloneBindings(bindings)
		if matchAll(branch, attrs, scratch) {
			for k, v := range scratch {
				if _, had := bindings[k]; !had {
					bindings[k] = v
				}
			}
			return true
		}
	}
	return false
}

// matchNot returns true when the inner clause group does NOT match.
// Scratch bindings are discarded — Not produces no new bindings.
func matchNot(n *factstore.Not, attrs map[string]any, bindings map[string]any) bool {
	scratch := cloneBindings(bindings)
	return !matchAll(n.Body, attrs, scratch)
}

// matchFullText scans the document's string-valued attributes for the
// query substring (case-insensitive). When Attribute is set, only that
// attribute is searched. Expr (raw Datalog query) is not interpreted —
// it's a Datalevin-specific escape hatch.
func matchFullText(f *factstore.FullText, attrs map[string]any) bool {
	if f.Query == "" {
		return false
	}
	needle := strings.ToLower(f.Query)
	if f.Attribute != "" {
		v, ok := attrs[f.Attribute]
		if !ok {
			return false
		}
		s, ok := v.(string)
		return ok && strings.Contains(strings.ToLower(s), needle)
	}
	for _, v := range attrs {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if strings.Contains(strings.ToLower(s), needle) {
			return true
		}
	}
	return false
}

func cloneBindings(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

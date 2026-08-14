package talondb

import (
	"strings"

	"github.com/opentalon/tln-language/internal/factstore"
)

// matchAllWithRules evaluates every clause against the document and
// bindings. RuleCall clauses consult the resolver (pre-built by
// Adapter.Query). A nil resolver fails RuleCall closed.
func matchAllWithRules(clauses []factstore.Clause, attrs map[string]any, bindings map[string]any, resolver *ruleResolution) bool {
	for _, c := range clauses {
		if !matchOneWithRules(c, attrs, bindings, resolver) {
			return false
		}
	}
	return true
}

// matchOneWithRules dispatches on clause type. Pattern, Predicate,
// Or, Not, FullText, and RuleCall (via a pre-built resolver) are
// supported.
func matchOneWithRules(c factstore.Clause, attrs map[string]any, bindings map[string]any, resolver *ruleResolution) bool {
	switch cc := c.(type) {
	case *factstore.Pattern:
		return matchPattern(cc, attrs, bindings)
	case *factstore.Predicate:
		return matchPredicate(cc, bindings)
	case *factstore.Or:
		return matchOrWithRules(cc, attrs, bindings, resolver)
	case *factstore.Not:
		return matchNotWithRules(cc, attrs, bindings, resolver)
	case *factstore.FullText:
		return matchFullText(cc, attrs)
	case *factstore.RuleCall:
		return matchRuleCall(cc, bindings, resolver)
	}
	return false
}

// matchRuleCall consults the pre-built resolver: the bound variable
// (call.Args[0]) must currently bind to a value in the resolver's
// allowed-set for this call. resolver==nil means no rules were
// pre-resolved; fail closed.
func matchRuleCall(call *factstore.RuleCall, bindings map[string]any, resolver *ruleResolution) bool {
	if resolver == nil {
		return false
	}
	allowed, ok := resolver.cachedAllowed(call)
	if !ok {
		return false
	}
	if len(call.Args) < 1 || !call.Args[0].IsVar() {
		return false
	}
	v, bound := bindings[call.Args[0].Var]
	if !bound {
		return false
	}
	return allowed[v]
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

// matchOrWithRules returns true when any branch matches. Per Datalog
// semantics, bindings made inside an Or branch must NOT leak to
// siblings — but they SHOULD leak to the enclosing query for
// variables that were previously unbound and that all surviving
// branches happen to bind to the same value. We follow MemoryStore's
// simpler rule: on the first successful branch, copy new bindings
// into the parent.
func matchOrWithRules(o *factstore.Or, attrs map[string]any, bindings map[string]any, resolver *ruleResolution) bool {
	for _, branch := range o.Branches {
		scratch := cloneBindings(bindings)
		if matchAllWithRules(branch, attrs, scratch, resolver) {
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

// matchNotWithRules returns true when the inner clause group does NOT
// match. Scratch bindings are discarded — Not produces no new
// bindings in the parent scope.
func matchNotWithRules(n *factstore.Not, attrs map[string]any, bindings map[string]any, resolver *ruleResolution) bool {
	scratch := cloneBindings(bindings)
	return !matchAllWithRules(n.Body, attrs, scratch, resolver)
}

// matchFullText scans the document's string-valued attributes for the
// query substring (case-insensitive). List-valued attributes are scanned
// element by element. When Attribute is set, only that attribute is
// searched. Expr (raw Datalog query) is not interpreted — it's a
// Datalevin-specific escape hatch.
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
		return fullTextValueMatches(v, needle)
	}
	for _, v := range attrs {
		if fullTextValueMatches(v, needle) {
			return true
		}
	}
	return false
}

// fullTextValueMatches reports whether one attribute value contains the
// (already lower-cased) needle, quantifying over list elements.
func fullTextValueMatches(v any, needle string) bool {
	switch val := v.(type) {
	case string:
		return strings.Contains(strings.ToLower(val), needle)
	case []string:
		for _, e := range val {
			if strings.Contains(strings.ToLower(e), needle) {
				return true
			}
		}
	case []any:
		for _, e := range val {
			s, ok := e.(string)
			if !ok {
				continue
			}
			if strings.Contains(strings.ToLower(s), needle) {
				return true
			}
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

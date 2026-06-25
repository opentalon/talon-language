package talondb

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/opentalon/talon-language/internal/factstore"
)

// splitPatterns categorises the Where clauses into anchors and
// var-bound patterns. Other clause types (Or, Not, FullText, RuleCall)
// return errors.ErrUnsupported — the planner audit confirmed the
// fleet_maintenance flow does not emit them.
func splitPatterns(clauses []factstore.Clause) (anchors, varPatterns []*factstore.Pattern, _ error) {
	for _, c := range clauses {
		switch p := c.(type) {
		case *factstore.Pattern:
			if p.Attribute == "" {
				return nil, nil, fmt.Errorf("talondb adapter: pattern with empty attribute")
			}
			switch {
			case p.Value.Literal != nil:
				anchors = append(anchors, p)
			case p.Value.Var != "":
				varPatterns = append(varPatterns, p)
			default:
				// Wildcard value — treat as var binding to ?e_anonymous.
				varPatterns = append(varPatterns, p)
			}
		case *factstore.Predicate:
			// Predicates are applied Go-side after binding.
		case *factstore.Or, *factstore.Not, *factstore.FullText, *factstore.RuleCall:
			return nil, nil, errors.ErrUnsupported
		default:
			return nil, nil, fmt.Errorf("talondb adapter: unknown clause type %T", c)
		}
	}
	return anchors, varPatterns, nil
}

// composeTerm produces the inverted-index key that talondb's term
// extractor emits for a (attribute, value) leaf.
//
// The extractor (talon-db/internal/index/terms.go) walks JSON. For a
// leaf value v at path [key], it emits the bare value v AND the
// composite "key:v". When the JSON key is a namespaced attribute like
// ":record/type", that whole string IS the last path segment — so the
// term it stores is ":record/type:item", not "type:item".
//
// This matches verbatim what factstore.Pattern carries (Attribute is
// already ":record/type", literal value "item"), so the lookup is a
// plain concatenation.
func composeTerm(attribute string, value any) string {
	return attribute + ":" + stringifyValue(value)
}

// stringifyValue matches the extractor's strconv.FormatFloat / bool /
// string formatting so the composite-term lookup hits the right bitmap.
func stringifyValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float32:
		return strconv.FormatFloat(float64(x), 'g', -1, 64)
	}
	return fmt.Sprint(v)
}

// intersectSorted returns the intersection of two sorted, deduplicated
// string slices.
func intersectSorted(a, b []string) []string {
	out := make([]string, 0, min(len(a), len(b)))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			i++
		case a[i] > b[j]:
			j++
		default:
			out = append(out, a[i])
			i++
			j++
		}
	}
	return out
}

// parseRecordIDOrString returns the docID as a float64 when it parses
// as an integer (matches MemoryStore's "?e is the entity id as a
// number" convention) and as a string otherwise.
func parseRecordIDOrString(docID string) any {
	if n, err := strconv.ParseInt(docID, 10, 64); err == nil {
		return float64(n)
	}
	return docID
}

// applyPredicates evaluates all Predicate clauses in the Where slice
// against the given bindings. Returns false if any predicate fails.
func applyPredicates(bindings map[string]any, clauses []factstore.Clause) bool {
	for _, c := range clauses {
		pred, ok := c.(*factstore.Predicate)
		if !ok {
			continue
		}
		left := resolveTerm(pred.Left, bindings)
		right := resolveTerm(pred.Right, bindings)
		if !evalPredicate(pred.Op, left, right) {
			return false
		}
	}
	return true
}

func resolveTerm(t factstore.Term, bindings map[string]any) any {
	if t.Var != "" {
		return bindings[t.Var]
	}
	return t.Literal
}

func evalPredicate(op string, left, right any) bool {
	switch op {
	case "==":
		return equalAny(left, right)
	case "!=":
		return !equalAny(left, right)
	case "<", "<=", ">", ">=":
		l, lok := numeric(left)
		r, rok := numeric(right)
		if !lok || !rok {
			return false
		}
		switch op {
		case "<":
			return l < r
		case "<=":
			return l <= r
		case ">":
			return l > r
		case ">=":
			return l >= r
		}
	case "starts_with":
		ls, lok := left.(string)
		rs, rok := right.(string)
		return lok && rok && strings.HasPrefix(ls, rs)
	case "ends_with":
		ls, lok := left.(string)
		rs, rok := right.(string)
		return lok && rok && strings.HasSuffix(ls, rs)
	case "contains":
		ls, lok := left.(string)
		rs, rok := right.(string)
		return lok && rok && strings.Contains(ls, rs)
	}
	return false
}

func equalAny(a, b any) bool {
	if a == b {
		return true
	}
	// Number normalisation: JSON decode produces float64, planner
	// literals may be int.
	if l, lok := numeric(a); lok {
		if r, rok := numeric(b); rok {
			return l == r
		}
	}
	return false
}

func numeric(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) {
			return 0, false
		}
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	}
	return 0, false
}

package talondb

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/opentalon/tln-language/internal/factstore"
)

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
		return stringPredicate(left, right, strings.HasPrefix)
	case "ends_with":
		return stringPredicate(left, right, strings.HasSuffix)
	case "contains":
		return stringPredicate(left, right, strings.Contains)
	}
	return false
}

// stringPredicate applies op to two string operands. A list-valued left
// operand quantifies existentially — the predicate holds if any element
// satisfies op. Non-string elements are skipped.
func stringPredicate(left, right any, op func(string, string) bool) bool {
	rs, rok := right.(string)
	if !rok {
		return false
	}
	switch list := left.(type) {
	case []string:
		for _, e := range list {
			if op(e, rs) {
				return true
			}
		}
		return false
	case []any:
		for _, e := range list {
			s, ok := e.(string)
			if !ok {
				continue
			}
			if op(s, rs) {
				return true
			}
		}
		return false
	}
	ls, lok := left.(string)
	return lok && op(ls, rs)
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

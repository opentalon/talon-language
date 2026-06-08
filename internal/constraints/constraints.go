// Package constraints enforces integrity-constraint blocks against incoming
// facts. See docs/constraints.md and issue #23.
//
// A `constraint` block defines an invariant that must hold for every record
// matching its selector. The Check function takes a candidate record and a
// set of constraint blocks and returns a Verdict telling the caller whether
// to accept, warn, quarantine, or reject the record.
//
// The evaluator handles the subset of conditions natural for a per-record
// check: comparisons, membership (`in [...]`), and boolean combinations.
// Cross-record / referential constraints (`references record where ...`) are
// not yet implemented — those require access to the full FactStore and will
// land alongside an EventEmitter-backed store implementation.
package constraints

import (
	"fmt"

	"github.com/opentalon/talon-language/internal/ast"
)

// Verdict is the outcome of running a record through a set of constraints.
//
// Multiple constraints may apply to the same record; the overall verdict is
// the most severe outcome observed: reject > quarantine > warn > accept.
type Verdict struct {
	Mode    string   // "accept" | "warn" | "quarantine" | "reject"
	Reasons []string // human-readable messages from violated constraints
}

// Check evaluates every constraint whose selector matches the record and
// returns the combined verdict.
func Check(record map[string]any, blocks []*ast.ConstraintBlock) Verdict {
	v := Verdict{Mode: "accept"}
	for _, c := range blocks {
		applies, err := matchSelector(c.Selector, record)
		if err != nil || !applies {
			continue
		}
		ok, err := evalCondition(c.Require, record)
		if err != nil {
			// Treat evaluation errors as accept with a warning — refusing to
			// store a fact because we couldn't decide is worse than logging.
			v = combine(v, Verdict{
				Mode:    "warn",
				Reasons: []string{fmt.Sprintf("constraint %q: %v", c.Name, err)},
			})
			continue
		}
		if !ok {
			msg := c.OnViolation.Message
			if msg == "" {
				msg = fmt.Sprintf("constraint %q violated", c.Name)
			}
			v = combine(v, Verdict{Mode: c.OnViolation.Mode, Reasons: []string{msg}})
		}
	}
	return v
}

// modeRank gives precedence to more severe modes; the highest-ranked mode
// observed across all violated constraints becomes the overall verdict.
func modeRank(m string) int {
	switch m {
	case "reject":
		return 3
	case "quarantine":
		return 2
	case "warn":
		return 1
	}
	return 0
}

func combine(a, b Verdict) Verdict {
	if modeRank(b.Mode) > modeRank(a.Mode) {
		a.Mode = b.Mode
	}
	a.Reasons = append(a.Reasons, b.Reasons...)
	return a
}

// matchSelector returns whether the constraint applies to this record. The
// constraint syntax requires `for records where <conditions>`; we evaluate
// those conditions in the same record-scoped evaluator the Require clause
// uses, so callers don't need a separate selector evaluator.
func matchSelector(sel ast.Selector, record map[string]any) (bool, error) {
	if len(sel.Conditions) == 0 {
		return true, nil
	}
	for _, c := range sel.Conditions {
		ok, err := evalCondition(c, record)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// evalCondition is a minimal per-record evaluator. It handles the conditions
// that make sense for a single-record integrity check; expressions that need
// a fact-graph traversal return an error so the verdict downgrades to warn.
func evalCondition(c ast.Condition, record map[string]any) (bool, error) {
	switch cc := c.(type) {
	case nil:
		return true, nil
	case *ast.LogicalCondition:
		left, err := evalCondition(cc.Left, record)
		if err != nil {
			return false, err
		}
		right, err := evalCondition(cc.Right, record)
		if err != nil {
			return false, err
		}
		switch cc.Op {
		case "and":
			return left && right, nil
		case "or":
			return left || right, nil
		}
		return false, fmt.Errorf("unknown logical operator %q", cc.Op)
	case *ast.NotCondition:
		ok, err := evalCondition(cc.Inner, record)
		if err != nil {
			return false, err
		}
		return !ok, nil
	case *ast.CompareCondition:
		l, err := evalExpr(cc.Left, record)
		if err != nil {
			return false, err
		}
		r, err := evalExpr(cc.Right, record)
		if err != nil {
			return false, err
		}
		return compare(l, cc.Op, r)
	case *ast.MembershipCondition:
		v, err := evalExpr(cc.Expr, record)
		if err != nil {
			return false, err
		}
		for _, m := range cc.Members {
			mv, err := evalExpr(m, record)
			if err != nil {
				return false, err
			}
			if equal(v, mv) {
				return !cc.Negated, nil
			}
		}
		return cc.Negated, nil
	case *ast.StringMatchCondition:
		// contains/starts_with/ends_with against a string attribute.
		v, err := evalExpr(cc.Subject, record)
		if err != nil {
			return false, err
		}
		s, ok := v.(string)
		if !ok {
			return false, fmt.Errorf("string match: subject is %T, not string", v)
		}
		return stringMatch(s, cc.Op, cc.Value), nil
	}
	return false, fmt.Errorf("constraint evaluator cannot handle condition type %T", c)
}

func evalExpr(e ast.Expr, record map[string]any) (any, error) {
	switch ee := e.(type) {
	case *ast.AttrExpr:
		return record[ee.Name], nil
	case *ast.IdentExpr:
		// Bare identifiers are treated as attribute references for constraint
		// evaluation — matches the way the language allows `status` to stand
		// in for `attr "status"` in shorthand condition syntax.
		return record[ee.Name], nil
	case *ast.LiteralExpr:
		return ee.Value, nil
	case *ast.UnaryExpr:
		// Only unary minus is meaningful for constraint values; the parser
		// emits `-10` as UnaryExpr("-", LiteralExpr(10)).
		v, err := evalExpr(ee.Operand, record)
		if err != nil {
			return nil, err
		}
		if ee.Op == "-" {
			if f, ok := toFloat(v); ok {
				return -f, nil
			}
		}
		return nil, fmt.Errorf("unary %s applied to %T", ee.Op, v)
	}
	return nil, fmt.Errorf("constraint evaluator cannot evaluate expression %T", e)
}

func compare(l any, op string, r any) (bool, error) {
	// Numeric compare when both sides are numbers; string equality otherwise.
	if lf, lok := toFloat(l); lok {
		if rf, rok := toFloat(r); rok {
			switch op {
			case "==":
				return lf == rf, nil
			case "!=":
				return lf != rf, nil
			case "<":
				return lf < rf, nil
			case "<=":
				return lf <= rf, nil
			case ">":
				return lf > rf, nil
			case ">=":
				return lf >= rf, nil
			}
		}
	}
	switch op {
	case "==":
		return equal(l, r), nil
	case "!=":
		return !equal(l, r), nil
	}
	return false, fmt.Errorf("cannot compare %T %s %T", l, op, r)
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func equal(a, b any) bool {
	if af, ok := toFloat(a); ok {
		if bf, ok := toFloat(b); ok {
			return af == bf
		}
	}
	return a == b
}

func stringMatch(s, op, needle string) bool {
	switch op {
	case "contains":
		return contains(s, needle)
	case "starts_with":
		return len(s) >= len(needle) && s[:len(needle)] == needle
	case "ends_with":
		return len(s) >= len(needle) && s[len(s)-len(needle):] == needle
	}
	return false
}

func contains(s, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

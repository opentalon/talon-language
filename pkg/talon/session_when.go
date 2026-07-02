package talon

import (
	"fmt"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/factstore"
)

// eventFields are the identifiers a session `when` clause may reference.
// Everything the clause names must be one of these or a literal — no
// cross-fact lookups in this version.
var eventFields = map[string]struct{}{
	"new_value":  {},
	"prev_value": {},
	"entity":     {},
}

// validateWhen checks an on-block's `when` clause against the v1 scope:
// comparisons of event fields (new_value / prev_value / entity) with
// literals, combined only with and/or. Anything else — cross-fact
// `attr "..."` lookups, aggregates, step/context refs, unknown
// identifiers — is rejected so a caller sees the limit at compile time
// rather than a silently-never-firing rule at runtime.
func validateWhen(on *ast.OnBlock) error {
	if on.When == nil {
		return nil
	}
	return checkCond(on.When)
}

func checkCond(c ast.Condition) error {
	switch cc := c.(type) {
	case *ast.LogicalCondition:
		if err := checkCond(cc.Left); err != nil {
			return err
		}
		return checkCond(cc.Right)
	case *ast.CompareCondition:
		if err := checkOperand(cc.Left); err != nil {
			return err
		}
		return checkOperand(cc.Right)
	default:
		return fmt.Errorf("unsupported when clause; this version allows only comparisons of new_value / prev_value / entity against literals (optionally combined with and/or)")
	}
}

func checkOperand(e ast.Expr) error {
	switch ex := e.(type) {
	case *ast.LiteralExpr:
		return nil
	case *ast.IdentExpr:
		if _, ok := eventFields[ex.Name]; ok {
			return nil
		}
		return fmt.Errorf("unknown identifier %q in when clause; use new_value, prev_value, or entity", ex.Name)
	default:
		return fmt.Errorf("unsupported operand in when clause; cross-fact lookups (e.g. attr \"...\") are not supported in this version")
	}
}

// evalWhen evaluates a validated `when` clause against an event. Because
// validateWhen has already run at NewSession time, only the supported
// shapes reach here.
func evalWhen(c ast.Condition, ev factstore.Event) (bool, error) {
	switch cc := c.(type) {
	case *ast.LogicalCondition:
		left, err := evalWhen(cc.Left, ev)
		if err != nil {
			return false, err
		}
		right, err := evalWhen(cc.Right, ev)
		if err != nil {
			return false, err
		}
		if cc.Op == "or" {
			return left || right, nil
		}
		return left && right, nil
	case *ast.CompareCondition:
		l := operandValue(cc.Left, ev)
		r := operandValue(cc.Right, ev)
		return compare(l, cc.Op, r)
	default:
		return false, fmt.Errorf("unsupported when clause")
	}
}

// operandValue resolves a when-clause operand to a concrete value: an
// event field for the allowed identifiers, or the literal value.
func operandValue(e ast.Expr, ev factstore.Event) any {
	switch ex := e.(type) {
	case *ast.LiteralExpr:
		return ex.Value
	case *ast.IdentExpr:
		switch ex.Name {
		case "new_value":
			return ev.Fact.Value
		case "prev_value":
			return ev.Prev.Value
		case "entity":
			return ev.Fact.RecordID
		}
	}
	return nil
}

// literalOf returns the constant value of an expression, if it is a
// literal. Used for the `on change ... to X` target.
func literalOf(e ast.Expr) (any, bool) {
	if lit, ok := e.(*ast.LiteralExpr); ok {
		return lit.Value, true
	}
	return nil, false
}

// compare applies a comparison operator to two values. Numbers are
// compared numerically (int/float coerced to float64); == and != also
// work for strings, bools, and nil. Ordering operators on non-numeric
// operands return an error, which surfaces on the Firing.
func compare(l any, op string, r any) (bool, error) {
	switch op {
	case "==":
		return valuesEqual(l, r), nil
	case "!=":
		return !valuesEqual(l, r), nil
	}

	lf, lok := toFloat(l)
	rf, rok := toFloat(r)
	if !lok || !rok {
		return false, fmt.Errorf("cannot compare %v %s %v: ordering requires numbers", l, op, r)
	}
	switch op {
	case "<":
		return lf < rf, nil
	case "<=":
		return lf <= rf, nil
	case ">":
		return lf > rf, nil
	case ">=":
		return lf >= rf, nil
	}
	return false, fmt.Errorf("unknown operator %q", op)
}

// valuesEqual compares two values for equality, coercing numeric kinds
// so that an int fact value equals a float literal (e.g. 0 == 0.0).
func valuesEqual(a, b any) bool {
	if af, aok := toFloat(a); aok {
		if bf, bok := toFloat(b); bok {
			return af == bf
		}
		return false
	}
	return a == b
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	}
	return 0, false
}

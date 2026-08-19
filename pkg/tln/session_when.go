package tln

import (
	"fmt"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/factstore"
)

// validateWhen checks an on-block's `when` clause: comparisons of an event
// field (new_value / prev_value / entity) OR a triggering-record field (any
// other bare identifier, e.g. `category`, `status`) with literals, combined
// only with and/or. Attr lookups, aggregates, and step/context refs are
// rejected so a caller sees the limit at compile time rather than a
// silently-never-firing rule at runtime.
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
	switch e.(type) {
	case *ast.LiteralExpr:
		return nil
	case *ast.IdentExpr:
		// new_value / prev_value / entity are event fields; any other bare
		// identifier is a field of the triggering record (e.g. `category`,
		// `status`), resolved from that record's row at evaluate time.
		return nil
	default:
		return fmt.Errorf("unsupported operand in when clause; use an event field " +
			"(new_value / prev_value / entity), a record field, or a literal")
	}
}

// evalWhen evaluates a validated `when` clause against an event. Because
// validateWhen has already run at NewSession time, only the supported
// shapes reach here.
func evalWhen(c ast.Condition, ev factstore.Event, row map[string]any) (bool, error) {
	switch cc := c.(type) {
	case *ast.LogicalCondition:
		left, err := evalWhen(cc.Left, ev, row)
		if err != nil {
			return false, err
		}
		right, err := evalWhen(cc.Right, ev, row)
		if err != nil {
			return false, err
		}
		if cc.Op == "or" {
			return left || right, nil
		}
		return left && right, nil
	case *ast.CompareCondition:
		l := operandValue(cc.Left, ev, row)
		r := operandValue(cc.Right, ev, row)
		return compare(l, cc.Op, r)
	default:
		return false, fmt.Errorf("unsupported when clause")
	}
}

// operandValue resolves a when-clause operand to a concrete value: an event
// field (new_value / prev_value / entity), a field of the triggering record
// (any other identifier, from the namespace-stripped row — e.g. `category`),
// or the literal value.
func operandValue(e ast.Expr, ev factstore.Event, row map[string]any) any {
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
		default:
			if row != nil {
				return row[ex.Name]
			}
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

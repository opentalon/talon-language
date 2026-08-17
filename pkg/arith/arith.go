// Package arith is the shared numeric kernel for the tln ecosystem. It applies
// arithmetic operators to int-or-float operands with one set of promotion rules,
// so tln core (float-valued expression evaluation) and the tln-prolog reasoner
// (int-preserving `is/2`) compute arithmetic identically instead of each
// carrying its own operator switch.
//
// It has no dependencies beyond the standard library so any module in the family
// can import it. Terms/records stay in the caller: this package only knows Num.
package arith

import (
	"errors"
	"math"
	"strconv"
)

// Num is an int-or-float numeric value. Integer operands stay integers through
// int-preserving operators (+, -, *) so a reasoner can tell 7 from 7.0; the
// float view is always available via Float.
type Num struct {
	isFloat bool
	i       int64
	f       float64
}

// Int builds an integer Num.
func Int(i int64) Num { return Num{i: i} }

// Float builds a float Num.
func Float(f float64) Num { return Num{isFloat: true, f: f} }

// IsFloat reports whether the value is a float.
func (n Num) IsFloat() bool { return n.isFloat }

// Float returns the value as a float64 (an int is widened).
func (n Num) Float() float64 {
	if n.isFloat {
		return n.f
	}
	return float64(n.i)
}

// Int returns the integer value; ok is false for a float Num.
func (n Num) Int() (int64, bool) {
	if n.isFloat {
		return 0, false
	}
	return n.i, true
}

// String renders the value in canonical form (integers without a decimal point).
func (n Num) String() string {
	if n.isFloat {
		return strconv.FormatFloat(n.f, 'g', -1, 64)
	}
	return strconv.FormatInt(n.i, 10)
}

var (
	// ErrDivByZero is returned by Binary for "/" and "//" with a zero divisor.
	ErrDivByZero = errors.New("division by zero")
	// ErrModByZero is returned by Binary for "mod"/"rem"/"%" with a zero divisor.
	ErrModByZero = errors.New("modulo by zero")
)

// Binary applies op to a and b. Promotion: +, -, * yield an integer when both
// operands are integers, otherwise a float. "/" is always float division. "//"
// is truncating integer division; "mod" is ISO modulo (sign follows the
// divisor); "rem" and "%" are the truncated remainder (sign follows the
// dividend) — "%" is an alias kept so tln core's operator maps unchanged.
func Binary(op string, a, b Num) (Num, error) {
	switch op {
	case "+":
		if bothInt(a, b) {
			return Int(a.i + b.i), nil
		}
		return Float(a.Float() + b.Float()), nil
	case "-":
		if bothInt(a, b) {
			return Int(a.i - b.i), nil
		}
		return Float(a.Float() - b.Float()), nil
	case "*":
		if bothInt(a, b) {
			return Int(a.i * b.i), nil
		}
		return Float(a.Float() * b.Float()), nil
	case "/":
		if b.Float() == 0 {
			return Num{}, ErrDivByZero
		}
		return Float(a.Float() / b.Float()), nil
	case "//":
		bi := toInt(b)
		if bi == 0 {
			return Num{}, ErrDivByZero
		}
		return Int(toInt(a) / bi), nil
	case "mod":
		bi := toInt(b)
		if bi == 0 {
			return Num{}, ErrModByZero
		}
		r := toInt(a) % bi
		if r != 0 && (r < 0) != (bi < 0) {
			r += bi
		}
		return Int(r), nil
	case "rem", "%":
		bi := toInt(b)
		if bi == 0 {
			return Num{}, ErrModByZero
		}
		return Int(toInt(a) % bi), nil
	}
	return Num{}, errors.New("arith: unknown binary operator " + strconv.Quote(op))
}

// Neg returns -a, preserving int/float.
func Neg(a Num) Num {
	if a.isFloat {
		return Float(-a.f)
	}
	return Int(-a.i)
}

// Abs returns |a|, preserving int/float.
func Abs(a Num) Num {
	if a.isFloat {
		return Float(math.Abs(a.f))
	}
	if a.i < 0 {
		return Int(-a.i)
	}
	return a
}

// Compare returns -1, 0, or 1 for a<b, a==b, a>b under numeric ordering.
func Compare(a, b Num) int {
	if bothInt(a, b) {
		switch {
		case a.i < b.i:
			return -1
		case a.i > b.i:
			return 1
		default:
			return 0
		}
	}
	af, bf := a.Float(), b.Float()
	switch {
	case af < bf:
		return -1
	case af > bf:
		return 1
	default:
		return 0
	}
}

func bothInt(a, b Num) bool { return !a.isFloat && !b.isFloat }

// toInt truncates a Num toward zero to an int64 for the integer-only operators.
func toInt(n Num) int64 {
	if n.isFloat {
		return int64(n.f)
	}
	return n.i
}

package talondb

import (
	"math"

	"github.com/opentalon/talon-language/internal/factstore"
)

// numericRangeBound is the half-bounded range a pushdown contributes.
// `lo` / `hi` use ±MaxFloat64 to mean "unbounded that side"; this
// keeps the LookupNumericRange RPC happy (it rejects NaN / Inf).
type numericRangeBound struct {
	lo, hi               float64
	loExclusive, hiExclusive bool
}

// merge intersects two bounds. Returns the tightest range across
// both. `ok` is false when the bounds are mutually exclusive
// (e.g. lo > hi after merging) — caller can short-circuit the query.
func (b numericRangeBound) merge(o numericRangeBound) (numericRangeBound, bool) {
	out := b
	if o.lo > out.lo || (o.lo == out.lo && o.loExclusive) {
		out.lo = o.lo
		out.loExclusive = o.loExclusive
	}
	if o.hi < out.hi || (o.hi == out.hi && o.hiExclusive) {
		out.hi = o.hi
		out.hiExclusive = o.hiExclusive
	}
	if out.lo > out.hi {
		return out, false
	}
	if out.lo == out.hi && (out.loExclusive || out.hiExclusive) {
		return out, false
	}
	return out, true
}

// fullRange is the identity range — accepts any finite numeric value.
func fullRange() numericRangeBound {
	return numericRangeBound{lo: -math.MaxFloat64, hi: math.MaxFloat64}
}

// detectNumericPushdowns walks the top-level Where clauses (not
// nested inside Or/Not) and returns a map of attribute → merged
// numeric range that can be pre-narrowed via LookupNumericRange.
//
// A predicate qualifies when:
//   - It uses one of `<`, `<=`, `>`, `>=`, `==`
//   - One side is a variable; the other is a numeric literal
//   - The variable is bound by a top-level Pattern of the shape
//     `(?e, literalAttr, ?var)` — i.e. the var carries a concrete
//     attribute's value, not derived state
//
// Returned attrs are the literal attribute names (e.g. ":attr/km").
// The second return is false when bounds are unsatisfiable — caller
// should short-circuit (no candidates can match).
func detectNumericPushdowns(clauses []factstore.Clause) (map[string]numericRangeBound, bool) {
	// Map var name → attribute it's bound to (from var-binding patterns).
	varToAttr := map[string]string{}
	for _, c := range clauses {
		p, ok := c.(*factstore.Pattern)
		if !ok {
			continue
		}
		if p.Attribute == "" || !p.Value.IsVar() {
			continue
		}
		// Skip when the value is already a literal (anchor case).
		varToAttr[p.Value.Var] = p.Attribute
	}

	ranges := map[string]numericRangeBound{}
	for _, c := range clauses {
		pred, ok := c.(*factstore.Predicate)
		if !ok {
			continue
		}
		varName, literal, op, ok := normalizePredicate(pred)
		if !ok {
			continue
		}
		attr, bound := varToAttr[varName]
		if !bound {
			continue
		}
		bb, ok := boundsFromOp(op, literal)
		if !ok {
			continue
		}
		existing, present := ranges[attr]
		if !present {
			existing = fullRange()
		}
		merged, satisfiable := existing.merge(bb)
		if !satisfiable {
			return nil, false
		}
		ranges[attr] = merged
	}
	return ranges, true
}

// normalizePredicate returns (varName, literalNumber, op) when the
// predicate is the shape we can push down. The op is normalised so
// the variable is always on the left:
//
//	?v > 10      → ("?v", 10, ">")
//	10 < ?v      → ("?v", 10, ">")
//	?v == 5      → ("?v", 5, "==")
//
// Non-numeric literals, both-vars predicates, or unsupported ops
// (string match, !=) return ok=false.
func normalizePredicate(p *factstore.Predicate) (string, float64, string, bool) {
	leftVar := p.Left.IsVar()
	rightVar := p.Right.IsVar()
	if leftVar == rightVar {
		// Both vars or both literals — nothing to push.
		return "", 0, "", false
	}

	var varName string
	var lit any
	var op string
	if leftVar {
		varName = p.Left.Var
		lit = p.Right.Literal
		op = p.Op
	} else {
		varName = p.Right.Var
		lit = p.Left.Literal
		// Flip the operator since the var moved to the left.
		op = flipOp(p.Op)
	}

	f, ok := pushdownFloat(lit)
	if !ok {
		return "", 0, "", false
	}
	switch op {
	case "<", "<=", ">", ">=", "==":
		return varName, f, op, true
	}
	return "", 0, "", false
}

func flipOp(op string) string {
	switch op {
	case "<":
		return ">"
	case "<=":
		return ">="
	case ">":
		return "<"
	case ">=":
		return "<="
	}
	return op
}

func boundsFromOp(op string, v float64) (numericRangeBound, bool) {
	r := fullRange()
	switch op {
	case "<":
		r.hi = v
		r.hiExclusive = true
	case "<=":
		r.hi = v
	case ">":
		r.lo = v
		r.loExclusive = true
	case ">=":
		r.lo = v
	case "==":
		r.lo = v
		r.hi = v
	default:
		return r, false
	}
	return r, true
}

func pushdownFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, false
		}
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

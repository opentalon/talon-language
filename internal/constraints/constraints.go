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
	"strconv"
	"strings"
	"time"

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
		ok, err := evalConditionAt(c.Require, record, time.Now().UTC())
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
		ok, err := evalConditionAt(c, record, time.Now().UTC())
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
func evalConditionAt(c ast.Condition, record map[string]any, now time.Time) (bool, error) {
	switch cc := c.(type) {
	case nil:
		return true, nil
	case *ast.LogicalCondition:
		left, err := evalConditionAt(cc.Left, record, now)
		if err != nil {
			return false, err
		}
		right, err := evalConditionAt(cc.Right, record, now)
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
		ok, err := evalConditionAt(cc.Inner, record, now)
		if err != nil {
			return false, err
		}
		return !ok, nil
	case *ast.CompareCondition:
		l, err := evalExpr(cc.Left, record, now)
		if err != nil {
			return false, err
		}
		r, err := evalExpr(cc.Right, record, now)
		if err != nil {
			return false, err
		}
		return compare(l, cc.Op, r)
	case *ast.MembershipCondition:
		v, err := evalExpr(cc.Expr, record, now)
		if err != nil {
			return false, err
		}
		for _, m := range cc.Members {
			mv, err := evalExpr(m, record, now)
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
		v, err := evalExpr(cc.Subject, record, now)
		if err != nil {
			return false, err
		}
		s, ok := v.(string)
		if !ok {
			return false, fmt.Errorf("string match: subject is %T, not string", v)
		}
		return stringMatch(s, cc.Op, cc.Value), nil
	case *ast.TemporalCondition:
		// `attr "x" older_than/newer_than N units` against a date-valued
		// attribute: older_than = date is before now-window; newer_than =
		// date is after now-window.
		v, err := evalExpr(cc.Subject, record, now)
		if err != nil {
			return false, err
		}
		d, ok := coerceTime(v)
		if !ok {
			return false, fmt.Errorf("temporal: %v is not a date", v)
		}
		cutoff := now.Add(-DurationDelta(cc.Value))
		switch cc.Op {
		case "older_than":
			return d.Before(cutoff), nil
		case "newer_than":
			return d.After(cutoff), nil
		}
		return false, fmt.Errorf("unknown temporal op %q", cc.Op)
	case *ast.AsOfCondition:
		// `was (...) N units ago` is a time-travel condition. The planner
		// lowers it into a QueryAsOf step + intersect; it is never a
		// per-row predicate, so reaching here is a planner bug.
		return false, fmt.Errorf("was...ago must be planned into a time-travel query, not evaluated per-row")
	}
	return false, fmt.Errorf("constraint evaluator cannot handle condition type %T", c)
}

// EvalCondition is the exported per-row condition evaluator. The
// testrunner's Filter step uses it to enforce `goConditions` the
// planner couldn't push into the FactStore query (arithmetic,
// cross-attribute comparison, temporal/date bounds). Returns true when
// the condition holds; errors surface for unresolvable expressions.
func EvalCondition(c ast.Condition, record map[string]any) (bool, error) {
	return evalConditionAt(c, record, time.Now().UTC())
}

// EvalConditionAt is EvalCondition with an explicit clock, so `today` /
// `older_than` / `approaching` evaluate deterministically (tests, and
// hosts that want a fixed evaluation instant).
func EvalConditionAt(c ast.Condition, record map[string]any, now time.Time) (bool, error) {
	return evalConditionAt(c, record, now)
}

func evalExpr(e ast.Expr, record map[string]any, now time.Time) (any, error) {
	switch ee := e.(type) {
	case *ast.TodayExpr:
		// Truncate to the day in UTC so date-only attribute values compare
		// cleanly against "today".
		return dateOnly(now), nil
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
		v, err := evalExpr(ee.Operand, record, now)
		if err != nil {
			return nil, err
		}
		if ee.Op == "-" {
			if f, ok := toFloat(v); ok {
				return -f, nil
			}
		}
		return nil, fmt.Errorf("unary %s applied to %T", ee.Op, v)
	case *ast.BinaryExpr:
		left, err := evalExpr(ee.Left, record, now)
		if err != nil {
			return nil, err
		}
		right, err := evalExpr(ee.Right, record, now)
		if err != nil {
			return nil, err
		}
		// Date arithmetic: `today + N units` (the `approaching` upper
		// bound desugars to BinaryExpr{TodayExpr, "+", Duration}).
		if lt, ok := left.(time.Time); ok {
			if dur, ok := right.(ast.Duration); ok && (ee.Op == "+" || ee.Op == "-") {
				delta := DurationDelta(dur)
				if ee.Op == "-" {
					delta = -delta
				}
				return lt.Add(delta), nil
			}
		}
		// Numeric arithmetic over attribute references — e.g.
		// `attr "km" > attr "last_service_km" + 20000`.
		lf, lok := toFloat(left)
		rf, rok := toFloat(right)
		if !lok || !rok {
			return nil, fmt.Errorf("binary %s on %T %T", ee.Op, left, right)
		}
		switch ee.Op {
		case "+":
			return lf + rf, nil
		case "-":
			return lf - rf, nil
		case "*":
			return lf * rf, nil
		case "/":
			if rf == 0 {
				return nil, fmt.Errorf("division by zero")
			}
			return lf / rf, nil
		case "%":
			if rf == 0 {
				return nil, fmt.Errorf("modulo by zero")
			}
			return float64(int64(lf) % int64(rf)), nil
		}
		return nil, fmt.Errorf("unknown binary op %q", ee.Op)
	case *ast.CallExpr:
		return evalCall(ee, record, now)
	}
	return nil, fmt.Errorf("constraint evaluator cannot evaluate expression %T", e)
}

// EvalExpr evaluates a value expression against a record's attributes, using
// the shared value evaluator that powers filter conditions. Exported so
// callers resolving expression-valued arguments (e.g. remediate MCP args)
// reuse one code path — arithmetic and the string builtins work everywhere
// expressions do.
func EvalExpr(e ast.Expr, record map[string]any, now time.Time) (any, error) {
	return evalExpr(e, record, now)
}

// evalCall evaluates a builtin function call (the string toolkit). Args are
// evaluated first, then dispatched by name. Arity mismatches are errors.
func evalCall(c *ast.CallExpr, record map[string]any, now time.Time) (any, error) {
	args := make([]any, len(c.Args))
	for i, a := range c.Args {
		v, err := evalExpr(a, record, now)
		if err != nil {
			return nil, err
		}
		args[i] = v
	}
	arity := func(n int) error {
		if len(args) != n {
			return fmt.Errorf("%s expects %d argument(s), got %d", c.Func, n, len(args))
		}
		return nil
	}
	switch c.Func {
	case "upper":
		if err := arity(1); err != nil {
			return nil, err
		}
		return strings.ToUpper(stringify(args[0])), nil
	case "lower":
		if err := arity(1); err != nil {
			return nil, err
		}
		return strings.ToLower(stringify(args[0])), nil
	case "trim":
		if err := arity(1); err != nil {
			return nil, err
		}
		return strings.TrimSpace(stringify(args[0])), nil
	case "length":
		if err := arity(1); err != nil {
			return nil, err
		}
		return float64(len([]rune(stringify(args[0])))), nil
	case "replace":
		if err := arity(3); err != nil {
			return nil, err
		}
		return strings.ReplaceAll(stringify(args[0]), stringify(args[1]), stringify(args[2])), nil
	case "concat":
		var b strings.Builder
		for _, a := range args {
			b.WriteString(stringify(a))
		}
		return b.String(), nil
	case "substring":
		if len(args) != 2 && len(args) != 3 {
			return nil, fmt.Errorf("substring expects 2 or 3 arguments, got %d", len(args))
		}
		runes := []rune(stringify(args[0]))
		start := clampIndex(toInt(args[1]), len(runes))
		end := len(runes)
		if len(args) == 3 {
			end = clampIndex(start+toInt(args[2]), len(runes))
		}
		return string(runes[start:end]), nil
	case "split":
		if err := arity(2); err != nil {
			return nil, err
		}
		parts := strings.Split(stringify(args[0]), stringify(args[1]))
		out := make([]any, len(parts))
		for i, p := range parts {
			out[i] = p
		}
		return out, nil
	case "join":
		if err := arity(2); err != nil {
			return nil, err
		}
		list, ok := args[0].([]any)
		if !ok {
			return nil, fmt.Errorf("join expects a list as its first argument, got %T", args[0])
		}
		parts := make([]string, len(list))
		for i, v := range list {
			parts[i] = stringify(v)
		}
		return strings.Join(parts, stringify(args[1])), nil
	}
	return nil, fmt.Errorf("unknown function %q", c.Func)
}

// stringify renders a value for string builtins: strings pass through, whole
// numbers drop their fractional part (so concat("v", 3) → "v3"), everything
// else uses default formatting.
func stringify(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case float64:
		if n == float64(int64(n)) {
			return strconv.FormatInt(int64(n), 10)
		}
		return strconv.FormatFloat(n, 'g', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

func toInt(v any) int {
	if f, ok := toFloat(v); ok {
		return int(f)
	}
	return 0
}

// clampIndex bounds a rune index to [0, n] so substring never panics.
func clampIndex(i, n int) int {
	if i < 0 {
		return 0
	}
	if i > n {
		return n
	}
	return i
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
	// Chronological compare when either side is a date (a time.Time, or a
	// date-formatted string) and the other coerces to a date too. Powers
	// `attr "d" >= today`, `attr "d" <= today + 7 days`, etc.
	if lt, rt, ok := bothDates(l, r); ok {
		switch op {
		case "==":
			return lt.Equal(rt), nil
		case "!=":
			return !lt.Equal(rt), nil
		case "<":
			return lt.Before(rt), nil
		case "<=":
			return !lt.After(rt), nil
		case ">":
			return lt.After(rt), nil
		case ">=":
			return !lt.Before(rt), nil
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

// bothDates coerces l and r to dates when at least one is already a
// time.Time (so a plain string-vs-string comparison isn't hijacked as a
// date compare). Returns ok=false otherwise.
func bothDates(l, r any) (time.Time, time.Time, bool) {
	_, lIsTime := l.(time.Time)
	_, rIsTime := r.(time.Time)
	if !lIsTime && !rIsTime {
		return time.Time{}, time.Time{}, false
	}
	lt, lok := coerceTime(l)
	rt, rok := coerceTime(r)
	if !lok || !rok {
		return time.Time{}, time.Time{}, false
	}
	return lt, rt, true
}

// coerceTime turns a time.Time or a date-formatted string into a
// day-truncated UTC time.
func coerceTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return dateOnly(x), true
	case string:
		for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01-02T15:04:05Z"} {
			if t, err := time.Parse(layout, x); err == nil {
				return dateOnly(t), true
			}
		}
	}
	return time.Time{}, false
}

// dateOnly truncates to midnight UTC so date-only values compare cleanly.
func dateOnly(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// DurationDelta converts an ast.Duration into a time.Duration (calendar
// months/years approximated as 30 / 365 days — fine for these bounds).
func DurationDelta(d ast.Duration) time.Duration {
	day := 24 * time.Hour
	switch d.Unit {
	case "hours", "hour":
		return time.Duration(d.Value) * time.Hour
	case "weeks", "week":
		return time.Duration(d.Value) * 7 * day
	case "months", "month":
		return time.Duration(d.Value) * 30 * day
	case "years", "year":
		return time.Duration(d.Value) * 365 * day
	default: // days
		return time.Duration(d.Value) * day
	}
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

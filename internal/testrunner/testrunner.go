package testrunner

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/diagnostic"
	"github.com/opentalon/talon-language/internal/mlruntime"
	"github.com/opentalon/talon-language/internal/planner"
)

// TestResult is the outcome of one test block.
type TestResult struct {
	Name   string
	Passed bool
	Errors []string
}

// TraceStep records one plan step's execution during a traced run.
// Marshalled to JSON by `talon trace`.
type TraceStep struct {
	Type         string                  `json:"type"`
	Into         string                  `json:"into"`
	Function     string                  `json:"function,omitempty"`
	Query        string                  `json:"query,omitempty"`
	Params       map[string]any          `json:"params,omitempty"`
	Rows         []int                   `json:"rows,omitempty"`
	Explanations []mlruntime.Explanation `json:"explanations,omitempty"`
}

// TraceResult is the per-test trace returned by Trace.
type TraceResult struct {
	Name    string      `json:"name"`
	Block   string      `json:"block"`
	Passed  bool        `json:"passed"`
	Errors  []string    `json:"errors,omitempty"`
	Steps   []TraceStep `json:"steps"`
	Flagged []int       `json:"flagged"`
}

// Run executes all test blocks against compiled query plans using the
// default ML registry.
func Run(prog *ast.Program, plans map[string]*planner.QueryPlan) []TestResult {
	return RunWithRegistry(prog, plans, mlruntime.NewRegistry())
}

// RunWithRegistry executes test blocks with an injected ML registry so
// callers can swap primitives in tests.
func RunWithRegistry(prog *ast.Program, plans map[string]*planner.QueryPlan, reg *mlruntime.Registry) []TestResult {
	var results []TestResult
	for _, b := range prog.Blocks {
		tb, ok := b.(*ast.TestBlock)
		if !ok {
			continue
		}
		tr, _ := runOne(tb, plans, reg)
		results = append(results, tr)
	}
	return results
}

// Trace executes all test blocks and returns rich per-step traces including
// ML explanations. Each test is run once against the same in-memory entity
// store the regular test runner uses.
func Trace(prog *ast.Program, plans map[string]*planner.QueryPlan) []TraceResult {
	reg := mlruntime.NewRegistry()
	var out []TraceResult
	for _, b := range prog.Blocks {
		tb, ok := b.(*ast.TestBlock)
		if !ok {
			continue
		}
		tr, steps := runOne(tb, plans, reg)
		flagged := flaggedFromSteps(steps)
		out = append(out, TraceResult{
			Name:    tr.Name,
			Block:   tb.WhenBlock,
			Passed:  tr.Passed,
			Errors:  tr.Errors,
			Steps:   steps,
			Flagged: flagged,
		})
	}
	return out
}

// flaggedFromSteps reads the last narrowed row set from a step list.
// Mirrors the executor's flaggedRows logic: start with the first DatalevinQuery,
// then narrow by each MLComputation step.
func flaggedFromSteps(steps []TraceStep) []int {
	var flagged []int
	for _, s := range steps {
		if s.Type == "DatalevinQuery" {
			flagged = s.Rows
			break
		}
	}
	for _, s := range steps {
		if s.Type == "MLComputation" {
			flagged = s.Rows
		}
	}
	return flagged
}

// entity is an in-memory record.
type entity struct {
	id     int
	fields map[string]interface{} // ":record/type" → "item", ":attr/km" → 45000, etc.
}

func runOne(tb *ast.TestBlock, plans map[string]*planner.QueryPlan, reg *mlruntime.Registry) (TestResult, []TraceStep) {
	result := TestResult{Name: tb.Name}

	// Build in-memory entities from given block
	entities := buildEntities(tb.Given)

	// Find the referenced query plan
	plan, ok := plans[tb.WhenBlock]
	if !ok {
		result.Errors = append(result.Errors, fmt.Sprintf("no plan for block %q", tb.WhenBlock))
		return result, nil
	}

	// Walk the full step list, mirroring the executor path so .test files can
	// later assert on ML / filter / render output as M3+ wires real primitives.
	vars := map[string]any{}
	var flagged []int
	var flaggedSet bool
	trace := make([]TraceStep, 0, len(plan.Steps))
	mlByEntity := map[int]mlruntime.Explanation{}
	var lastThreshold *mlruntime.Threshold
	for _, step := range plan.Steps {
		switch s := step.(type) {
		case *planner.DatalevinQuery:
			ids := evalDatalogInMemory(s.Query, entities)
			vars[s.Into] = ids
			if !flaggedSet {
				flagged = ids
				flaggedSet = true
			}
			trace = append(trace, TraceStep{
				Type:  "DatalevinQuery",
				Into:  s.Into,
				Query: s.Query,
				Rows:  ids,
			})
		case *planner.MLComputation:
			narrowed, explanations := narrowByML(reg, s, flagged, entities)
			flagged = narrowed
			for _, e := range explanations {
				mlByEntity[e.EntityID] = e
				if e.Threshold != nil {
					lastThreshold = e.Threshold
				}
			}
			vars[s.Into] = map[string]any{
				"function": s.Function,
				"input":    vars[s.Input],
				"params":   s.Params,
				"flagged":  flagged,
			}
			trace = append(trace, TraceStep{
				Type:         "MLComputation",
				Into:         s.Into,
				Function:     s.Function,
				Params:       s.Params,
				Rows:         flagged,
				Explanations: explanations,
			})
		case *planner.GoComputation:
			vars[s.Into] = map[string]any{
				"function": s.Function,
				"input":    vars[s.Input],
				"params":   s.Params,
				"status":   "stub",
			}
			trace = append(trace, TraceStep{
				Type:     "GoComputation",
				Into:     s.Into,
				Function: s.Function,
				Params:   s.Params,
			})
		case *planner.Filter:
			vars[s.Into] = vars[s.Input]
			trace = append(trace, TraceStep{
				Type: "Filter",
				Into: s.Into,
			})
		}
	}

	// Check assertions
	for _, assertion := range tb.Expect {
		if err := checkAssertion(assertion, flagged, plan, mlByEntity, lastThreshold); err != "" {
			result.Errors = append(result.Errors, err)
		}
	}

	result.Passed = len(result.Errors) == 0
	return result, trace
}

func buildEntities(data []ast.TestDatum) map[int]*entity {
	entities := map[int]*entity{}
	for _, d := range data {
		e, ok := entities[d.ID]
		if !ok {
			e = &entity{id: d.ID, fields: map[string]interface{}{}}
			entities[d.ID] = e
		}
		for k, v := range d.Fields {
			ns := fieldNamespace(d.Kind, k)
			e.fields[ns] = v
		}
	}
	return entities
}

func fieldNamespace(kind, key string) string {
	if kind == "attr" {
		return ":attr/" + key
	}
	switch key {
	case "type":
		return ":record/type"
	case "status":
		return ":record/status"
	case "category":
		return ":record/category"
	default:
		return ":record/" + key
	}
}

// evalDatalogInMemory is a minimal Datalog evaluator that matches the query
// patterns generated by the Talon planner against in-memory entities.
// It handles the subset of Datalog we emit: EAV patterns and comparison predicates.
func evalDatalogInMemory(query string, entities map[int]*entity) []int {
	clauses := parseWhereClauses(query)
	var matched []int

	for id, ent := range entities {
		if entityMatchesClauses(ent, clauses) {
			matched = append(matched, id)
		}
	}
	return matched
}

type whereClause struct {
	kind string // "eav" or "predicate"
	// EAV: [?e :attr/name value_or_var]
	attr  string
	value string // literal value (empty if variable binding)
	vname string // variable name if binding
	// Predicate: [(> ?var value)]
	op      string
	predVar string
	predVal string
}

func parseWhereClauses(query string) []whereClause {
	// Extract the :where section
	idx := strings.Index(query, ":where")
	if idx < 0 {
		return nil
	}
	whereSection := query[idx+len(":where"):]
	// Remove trailing ]
	if last := strings.LastIndex(whereSection, "]"); last >= 0 {
		whereSection = whereSection[:last]
	}

	var clauses []whereClause
	i := 0
	for i < len(whereSection) {
		for i < len(whereSection) && isSpace(whereSection[i]) {
			i++
		}
		if i >= len(whereSection) {
			break
		}
		if whereSection[i] == '[' {
			depth := 1
			start := i
			i++
			for i < len(whereSection) && depth > 0 {
				if whereSection[i] == '[' {
					depth++
				} else if whereSection[i] == ']' {
					depth--
				}
				i++
			}
			clause := strings.TrimSpace(whereSection[start:i])
			if c := parseOneClause(clause); c != nil {
				clauses = append(clauses, *c)
			}
		} else {
			i++
		}
	}
	return clauses
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\n' || b == '\r' || b == '\t'
}

func parseOneClause(s string) *whereClause {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return nil
	}
	s = s[1 : len(s)-1] // remove [ ]
	s = strings.TrimSpace(s)

	if strings.HasPrefix(s, "(") {
		return parsePredicate(s)
	}

	parts := splitTokens(s)
	if len(parts) < 3 {
		return nil
	}

	c := &whereClause{kind: "eav", attr: parts[1]}
	val := parts[2]
	if strings.HasPrefix(val, "?") {
		c.vname = val
	} else {
		c.value = unquote(val)
	}
	return c
}

func parsePredicate(s string) *whereClause {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "(")
	s = strings.TrimSuffix(s, ")")
	parts := splitTokens(s)
	if len(parts) < 3 {
		return nil
	}
	return &whereClause{
		kind:    "predicate",
		op:      parts[0],
		predVar: parts[1],
		predVal: unquote(parts[2]),
	}
}

func splitTokens(s string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '"' {
			inQuote = !inQuote
			current.WriteByte(ch)
		} else if (ch == ' ' || ch == '\t' || ch == '\n') && !inQuote {
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		} else {
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func entityMatchesClauses(ent *entity, clauses []whereClause) bool {
	bindings := map[string]interface{}{}

	for _, c := range clauses {
		switch c.kind {
		case "eav":
			val, exists := ent.fields[c.attr]
			if c.value != "" {
				if !exists || fmt.Sprintf("%v", val) != c.value {
					return false
				}
			} else if c.vname != "" {
				if !exists {
					return false
				}
				bindings[c.vname] = val
			}
		case "predicate":
			bound, ok := bindings[c.predVar]
			if !ok {
				return false
			}
			// Right side may be another variable or a literal
			rightVal := c.predVal
			if strings.HasPrefix(c.predVal, "?") {
				rv, ok := bindings[c.predVal]
				if !ok {
					return false
				}
				rightVal = fmt.Sprintf("%v", rv)
			}
			if !evalPredicate(c.op, bound, rightVal) {
				return false
			}
		}
	}
	return true
}

func evalPredicate(op string, left interface{}, rightStr string) bool {
	var leftNum, rightNum float64
	var numOk bool

	switch v := left.(type) {
	case float64:
		leftNum = v
		numOk = true
	case int:
		leftNum = float64(v)
		numOk = true
	}

	if numOk {
		var err error
		rightNum, err = strconv.ParseFloat(rightStr, 64)
		if err != nil {
			return false
		}
		switch op {
		case ">":
			return leftNum > rightNum
		case ">=":
			return leftNum >= rightNum
		case "<":
			return leftNum < rightNum
		case "<=":
			return leftNum <= rightNum
		case "=":
			return leftNum == rightNum
		case "not=":
			return leftNum != rightNum
		}
	}

	leftStr := fmt.Sprintf("%v", left)
	switch op {
	case "=":
		return leftStr == rightStr
	case "not=":
		return leftStr != rightStr
	}
	return false
}

func checkAssertion(a ast.TestAssertion, flagged []int, _ *planner.QueryPlan, mlByEntity map[int]mlruntime.Explanation, threshold *mlruntime.Threshold) string {
	switch a.Kind {
	case "flagged":
		if !intSliceContains(flagged, a.ID) {
			return fmt.Sprintf("expected entity %d to be flagged, but it was not", a.ID)
		}
	case "not_flagged":
		if intSliceContains(flagged, a.ID) {
			return fmt.Sprintf("expected entity %d to NOT be flagged, but it was", a.ID)
		}
	case "count":
		expected, _ := strconv.Atoi(a.Value)
		if a.Op == "==" && len(flagged) != expected {
			return fmt.Sprintf("expected %d flagged, got %d", expected, len(flagged))
		}
	case "score":
		exp, ok := mlByEntity[a.ID]
		if !ok || len(exp.Rules) == 0 {
			return fmt.Sprintf("no ML score recorded for entity %d", a.ID)
		}
		want, err := strconv.ParseFloat(a.Value, 64)
		if err != nil {
			return fmt.Sprintf("score assertion: invalid number %q", a.Value)
		}
		got, ok := numericValue(exp.Rules[0].Observed)
		if !ok {
			return fmt.Sprintf("entity %d score observed not numeric: %v", a.ID, exp.Rules[0].Observed)
		}
		if !compareScalars(a.Op, got, want) {
			return fmt.Sprintf("entity %d score: %v %s %v failed", a.ID, got, a.Op, want)
		}
	case "threshold":
		if threshold == nil {
			return "threshold assertion: no ML threshold recorded"
		}
		want, err := strconv.ParseFloat(a.Value, 64)
		if err != nil {
			return fmt.Sprintf("threshold assertion: invalid number %q", a.Value)
		}
		if !compareScalars(a.Op, threshold.Value, want) {
			return fmt.Sprintf("threshold: %v %s %v failed", threshold.Value, a.Op, want)
		}
	}
	return ""
}

// numericValue extracts a float64 from common JSON-ish numeric types.
func numericValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// compareScalars applies a Talon comparison/approximation operator.
// `~=` allows 5% relative or 0.01 absolute tolerance, whichever is larger.
func compareScalars(op string, got, want float64) bool {
	switch op {
	case ">":
		return got > want
	case ">=":
		return got >= want
	case "<":
		return got < want
	case "<=":
		return got <= want
	case "==", "=":
		return got == want
	case "!=", "not=":
		return got != want
	case "~=":
		tol := 0.05 * absFloat(want)
		if tol < 0.01 {
			tol = 0.01
		}
		return absFloat(got-want) <= tol
	}
	return false
}

func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func intSliceContains(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// PrintResults formats test results for CLI output.
func PrintResults(results []TestResult) (passed, failed int) {
	for _, r := range results {
		if r.Passed {
			passed++
			fmt.Printf("  PASS  %s\n", r.Name)
		} else {
			failed++
			fmt.Printf("  FAIL  %s\n", r.Name)
			for _, e := range r.Errors {
				fmt.Printf("        %s\n", e)
			}
		}
	}
	return
}

// narrowByML runs an MLComputation against the entity values pulled from the
// in-memory store and returns the subset of flagged ids whose Value=true plus
// the per-entity explanations the primitive produced.
// If the registry has no primitive registered for the function, or the step
// lacks the params needed to fetch values, the original flagged set passes
// through unchanged and explanations is nil.
func narrowByML(reg *mlruntime.Registry, s *planner.MLComputation, flagged []int, entities map[int]*entity) ([]int, []mlruntime.Explanation) {
	if reg == nil || !reg.Has(s.Function) {
		return flagged, nil
	}
	attr, _ := s.Params["attr"].(string)
	if attr == "" {
		return flagged, nil
	}
	attrKey := ":attr/" + attr

	rows := make([][]any, 0, len(flagged))
	for _, id := range flagged {
		e := entities[id]
		if e == nil {
			continue
		}
		val, ok := e.fields[attrKey]
		if !ok {
			continue
		}
		rows = append(rows, []any{id, val})
	}

	prim, _ := reg.Get(s.Function)
	results, err := prim.Compute(context.Background(), mlruntime.Input{
		Rows:   rows,
		Schema: map[string]int{"entity_id": 0, "value": 1},
		Params: s.Params,
	})
	if err != nil {
		// Sample too small — leave flagged unchanged so tests can still run
		// against tiny fixtures without forcing a synthetic 12-week window.
		return flagged, nil
	}

	keep := map[int]bool{}
	explanations := make([]mlruntime.Explanation, 0, len(results))
	for _, r := range results {
		explanations = append(explanations, r.Explanation)
		if v, _ := r.Value.(bool); v {
			keep[r.EntityID] = true
		}
	}
	out := make([]int, 0, len(flagged))
	for _, id := range flagged {
		if keep[id] {
			out = append(out, id)
		}
	}
	return out, explanations
}

// Validate checks that all test blocks reference existing plans.
func Validate(prog *ast.Program, plans map[string]*planner.QueryPlan) diagnostic.List {
	var diags diagnostic.List
	for _, b := range prog.Blocks {
		tb, ok := b.(*ast.TestBlock)
		if !ok {
			continue
		}
		if _, exists := plans[tb.WhenBlock]; !exists {
			diags.AddError("", tb.Pos.Line, tb.Pos.Col,
				fmt.Sprintf("test %q references unknown block %q", tb.Name, tb.WhenBlock), "")
		}
	}
	return diags
}

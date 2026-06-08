package testrunner

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/diagnostic"
	"github.com/opentalon/talon-language/internal/factstore"
	"github.com/opentalon/talon-language/internal/mlruntime"
	"github.com/opentalon/talon-language/internal/planner"
)

// TestResult is the outcome of one test block.
type TestResult struct {
	Name     string
	Passed   bool
	Errors   []string
	Duration time.Duration
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
	tunings := computeTunings(prog, plans)
	progBlocks := indexProgBlocks(prog)
	var results []TestResult
	for _, b := range prog.Blocks {
		tb, ok := b.(*ast.TestBlock)
		if !ok {
			continue
		}
		tr, _ := runOneTuned(tb, plans, reg, tunings, progBlocks)
		results = append(results, tr)
	}
	return results
}

// indexProgBlocks builds a name→Block index of every non-test block in
// the program. Used by the tuning + Decision pathways to look up the
// AST block being executed.
func indexProgBlocks(prog *ast.Program) map[string]ast.Block {
	m := map[string]ast.Block{}
	for _, b := range prog.Blocks {
		if _, isTest := b.(*ast.TestBlock); isTest {
			continue
		}
		m[b.BlockName()] = b
	}
	return m
}

// Trace executes all test blocks and returns rich per-step traces including
// ML explanations. Each test is run once against the same in-memory entity
// store the regular test runner uses.
func Trace(prog *ast.Program, plans map[string]*planner.QueryPlan) []TraceResult {
	reg := mlruntime.NewRegistry()
	tunings := computeTunings(prog, plans)
	progBlocks := indexProgBlocks(prog)
	var out []TraceResult
	for _, b := range prog.Blocks {
		tb, ok := b.(*ast.TestBlock)
		if !ok {
			continue
		}
		tr, steps := runOneTuned(tb, plans, reg, tunings, progBlocks)
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

// runOneTuned is the full-context runner: tunings carries any auto-tuned
// parameters (block name → *tuningResult) the testrunner discovered for
// detect blocks with `tune against test "..."` clauses. progBlocks is the
// name→Block index used to look up the active block for tuning lookup.
func runOneTuned(
	tb *ast.TestBlock,
	plans map[string]*planner.QueryPlan,
	reg *mlruntime.Registry,
	tunings map[string]*tuningResult,
	progBlocks map[string]ast.Block,
) (TestResult, []TraceStep) {
	result := TestResult{Name: tb.Name}
	start := time.Now()

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
		case *planner.GraphSnapshot:
			snap := buildGraphFromEntities(entities)
			vars[s.Into] = snap
			trace = append(trace, TraceStep{
				Type: "GraphSnapshot",
				Into: s.Into,
			})
		case *planner.MLComputation:
			// PPR is special-cased: it consumes a GraphSnapshot from vars,
			// not just the upstream candidate set, so it has its own runner.
			if s.Function == planner.FuncPPRTopK {
				narrowed, explanations := runPPR(reg, s, flagged, vars, entities)
				flagged = narrowed
				for _, e := range explanations {
					mlByEntity[e.EntityID] = e
				}
				vars[s.Into] = map[string]any{
					"function": s.Function,
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
				continue
			}
			// If the test's WhenBlock is a detect with a `tune against test`
			// clause, ABC has already discovered the optimal parameter against
			// the labeled fixture; inject it (matching by primitive function)
			// before running the step.
			stepToRun := s
			if tunings != nil {
				if tr, ok := tunings[tb.WhenBlock]; ok && tr.Function == s.Function {
					cp := *s
					cp.Params = cloneParams(s.Params)
					cp.Params[tr.ParamName] = tr.ParamValue
					stepToRun = &cp
				}
			}
			narrowed, explanations := narrowByML(reg, stepToRun, flagged, entities)
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
				"params":   stepToRun.Params,
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
			if s.Function == planner.FuncOptimizePareto {
				out := narrowByPareto(s, flagged, entities)
				if out.frontier != nil {
					flagged = out.frontier
				}
				vars[s.Into] = out.result
				trace = append(trace, TraceStep{
					Type:     "GoComputation",
					Into:     s.Into,
					Function: s.Function,
					Params:   s.Params,
					Rows:     flagged,
				})
				break
			}
			if s.Function == planner.FuncOptimizeGA {
				out := narrowByGA(s, flagged, entities)
				if out.flagged != nil {
					flagged = out.flagged
				}
				vars[s.Into] = out.result
				trace = append(trace, TraceStep{
					Type:     "GoComputation",
					Into:     s.Into,
					Function: s.Function,
					Params:   s.Params,
					Rows:     flagged,
				})
				break
			}
			if s.Function == planner.FuncOptimizeACO {
				out := narrowByACO(s, flagged, entities)
				if out.flagged != nil {
					flagged = out.flagged
				}
				vars[s.Into] = out.result
				trace = append(trace, TraceStep{
					Type:     "GoComputation",
					Into:     s.Into,
					Function: s.Function,
					Params:   s.Params,
					Rows:     flagged,
				})
				break
			}
			if s.Function == planner.FuncOptimizeILP {
				out := narrowByILP(s, flagged, entities)
				if out.flagged != nil {
					flagged = out.flagged
				}
				vars[s.Into] = out.result
				trace = append(trace, TraceStep{
					Type:     "GoComputation",
					Into:     s.Into,
					Function: s.Function,
					Params:   s.Params,
					Rows:     flagged,
				})
				break
			}
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
	result.Duration = time.Since(start)
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

// PrintResults formats test results for CLI output. When verbose is true,
// each test prints PASS/FAIL with duration; otherwise only FAIL lines and
// their error detail are printed (mirrors `go test` defaults).
func PrintResults(results []TestResult, verbose bool) (passed, failed int) {
	for _, r := range results {
		if r.Passed {
			passed++
			if verbose {
				fmt.Printf("  PASS  %s (%s)\n", r.Name, fmtDuration(r.Duration))
			}
		} else {
			failed++
			if verbose {
				fmt.Printf("  FAIL  %s (%s)\n", r.Name, fmtDuration(r.Duration))
			} else {
				fmt.Printf("  FAIL  %s\n", r.Name)
			}
			for _, e := range r.Errors {
				fmt.Printf("        %s\n", e)
			}
		}
	}
	return
}

func fmtDuration(d time.Duration) string {
	if d < time.Microsecond {
		return fmt.Sprintf("%dns", d.Nanoseconds())
	}
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	return fmt.Sprintf("%dms", d.Milliseconds())
}

// FilterByName returns the subset of results whose names contain the given
// substring. Empty pattern returns input unchanged.
func FilterByName(results []TestResult, pattern string) []TestResult {
	if pattern == "" {
		return results
	}
	out := results[:0:0]
	for _, r := range results {
		if strings.Contains(r.Name, pattern) {
			out = append(out, r)
		}
	}
	return out
}

// JUnitSuite groups the results for one .talon.test file.
type JUnitSuite struct {
	File    string
	Results []TestResult
}

type junitTestsuites struct {
	XMLName xml.Name         `xml:"testsuites"`
	Suites  []junitTestsuite `xml:"testsuite"`
}

type junitTestsuite struct {
	Name     string          `xml:"name,attr"`
	Tests    int             `xml:"tests,attr"`
	Failures int             `xml:"failures,attr"`
	Time     string          `xml:"time,attr"`
	Cases    []junitTestcase `xml:"testcase"`
}

type junitTestcase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

// WriteJUnit writes a JUnit-style XML report covering all given suites.
func WriteJUnit(w io.Writer, suites []JUnitSuite) error {
	var doc junitTestsuites
	for _, s := range suites {
		ts := junitTestsuite{Name: s.File, Tests: len(s.Results)}
		var total time.Duration
		for _, r := range s.Results {
			tc := junitTestcase{
				Name:      r.Name,
				Classname: s.File,
				Time:      fmt.Sprintf("%.3f", r.Duration.Seconds()),
			}
			if !r.Passed {
				ts.Failures++
				msg := strings.Join(r.Errors, "; ")
				tc.Failure = &junitFailure{
					Message: msg,
					Body:    strings.Join(r.Errors, "\n"),
				}
			}
			ts.Cases = append(ts.Cases, tc)
			total += r.Duration
		}
		ts.Time = fmt.Sprintf("%.3f", total.Seconds())
		doc.Suites = append(doc.Suites, ts)
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
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

// buildGraphFromEntities projects the in-memory test entities into a
// factstore.GraphSnapshot used by the PPR primitive. We ignore record
// type metadata; every (attribute, value) pair contributes to the graph.
func buildGraphFromEntities(entities map[int]*entity) *factstore.GraphSnapshot {
	triples := make([]factstore.FactTriple, 0, len(entities)*4)
	for id, e := range entities {
		idStr := strconv.Itoa(id)
		for attr, val := range e.fields {
			triples = append(triples, factstore.FactTriple{
				Entity:    idStr,
				Attribute: attr,
				Value:     val,
			})
		}
	}
	// Exclude high-fanout/low-information attributes that would otherwise
	// connect every record in the selector to every other (type, status,
	// per-entity unique names). The test fixture's discriminating signal
	// lives in :record/category and :attr/* — the rest is noise.
	return factstore.BuildSnapshotFromTriples(triples, 1, factstore.SnapshotOptions{
		ExcludeAttrs: []string{":record/type", ":record/status", ":attr/name"},
	})
}

// runPPR drives the PPR primitive from the test runner, resolving seed
// expressions against the candidate row set and surfacing top-K entity IDs
// + per-entity Explanations.
func runPPR(reg *mlruntime.Registry, s *planner.MLComputation, candidates []int, vars map[string]any, _ map[int]*entity) ([]int, []mlruntime.Explanation) {
	if reg == nil || !reg.Has(s.Function) {
		return candidates, nil
	}
	prim, _ := reg.Get(s.Function)

	graphVar, _ := s.Params["graph_var"].(string)
	graph, _ := vars[graphVar].(*factstore.GraphSnapshot)
	if graph == nil {
		return candidates, nil
	}

	seeds := []string{}
	if e, ok := s.Params["seed_expr"].(ast.Expr); ok && e != nil {
		seeds = append(seeds, resolveSeedExpr(e, candidates)...)
	}
	if exprs, ok := s.Params["seeds_expr"].([]ast.Expr); ok {
		for _, e := range exprs {
			seeds = append(seeds, resolveSeedExpr(e, candidates)...)
		}
	}
	if len(seeds) == 0 {
		return candidates, nil
	}

	params := map[string]any{
		"graph": graph,
		"seeds": seeds,
	}
	for _, k := range []string{"top_k", "damping", "tolerance", "max_iterations", "include_seeds"} {
		if v, ok := s.Params[k]; ok && v != nil {
			params[k] = v
		}
	}

	results, err := prim.Compute(context.Background(), mlruntime.Input{Params: params})
	if err != nil {
		return candidates, nil
	}

	keep := map[int]bool{}
	explanations := make([]mlruntime.Explanation, 0, len(results))
	for _, r := range results {
		explanations = append(explanations, r.Explanation)
		// Prefer the original numeric entity string when available.
		if ent, _ := r.Explanation.Inputs["entity"].(string); ent != "" {
			if id, err := strconv.Atoi(ent); err == nil {
				keep[id] = true
				continue
			}
		}
		keep[r.EntityID] = true
	}
	out := make([]int, 0, len(keep))
	for _, id := range candidates {
		if keep[id] {
			out = append(out, id)
		}
	}
	// If the selector didn't include the top-K nodes, fall back to whatever
	// IDs PPR did select — this keeps `find related` standalone blocks
	// usable in tests without a pre-narrowed candidate list.
	if len(out) == 0 {
		for id := range keep {
			out = append(out, id)
		}
	}
	return out, explanations
}

func resolveSeedExpr(e ast.Expr, candidates []int) []string {
	switch v := e.(type) {
	case *ast.LiteralExpr:
		switch x := v.Value.(type) {
		case string:
			return []string{x}
		case float64:
			return []string{strconv.FormatInt(int64(x), 10)}
		case int:
			return []string{strconv.Itoa(x)}
		}
	case *ast.AttrExpr:
		out := make([]string, 0, len(candidates))
		for _, id := range candidates {
			out = append(out, strconv.Itoa(id))
		}
		return out
	case *ast.IdentExpr:
		return []string{v.Name}
	case *ast.ListExpr:
		out := []string{}
		for _, el := range v.Elements {
			out = append(out, resolveSeedExpr(el, candidates)...)
		}
		return out
	}
	return nil
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

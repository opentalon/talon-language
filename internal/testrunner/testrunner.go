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
	"github.com/opentalon/talon-language/internal/constraints"
	"github.com/opentalon/talon-language/internal/diagnostic"
	"github.com/opentalon/talon-language/internal/factstore"
	talonlog "github.com/opentalon/talon-language/internal/log"
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
// Mirrors the executor's flaggedRows logic: start with the first FactQuery,
// then narrow by each MLComputation step.
func flaggedFromSteps(steps []TraceStep) []int {
	var flagged []int
	for _, s := range steps {
		if s.Type == "FactQuery" {
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
		case *planner.FactQuery:
			ids := evalQueryInMemory(s.Query, entities)
			vars[s.Into] = ids
			if !flaggedSet {
				flagged = ids
				flaggedSet = true
			}
			trace = append(trace, TraceStep{
				Type:  "FactQuery",
				Into:  s.Into,
				Query: s.Query.String(),
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
			// Apply the deferred-to-Go conditions to each flagged row,
			// using the shared constraint evaluator extended with
			// BinaryExpr arithmetic. Conditions the evaluator can't
			// resolve fall back to keeping the row (better to over-
			// include than to silently drop everything when one expr
			// shape isn't supported yet).
			kept := applyFilterConditions(s.Conditions, flagged, entities)
			flagged = kept
			vars[s.Into] = kept
			trace = append(trace, TraceStep{
				Type: "Filter",
				Into: s.Into,
				Rows: kept,
			})
		case *planner.RecordSequenceStep:
			kept := narrowByRecordSequence(s, flagged, entities)
			flagged = kept
			vars[s.Into] = kept
			trace = append(trace, TraceStep{
				Type: "RecordSequenceStep",
				Into: s.Into,
				Rows: kept,
			})
		}
	}

	// Check assertions
	for _, assertion := range tb.Expect {
		if err := checkAssertion(assertion, flagged, plan, mlByEntity, lastThreshold); err != "" {
			result.Errors = append(result.Errors, err)
		}
	}

	// MCP mocking: when a test stubs or asserts MCP calls, run the block
	// through the real executor (with a recording caller seeded from the
	// mocks) so remediate / enrich / workflow mcp steps actually fire,
	// then verify the mcp_called assertions. This is additive — tests
	// without mock/mcp_called clauses are unaffected.
	if len(tb.Mocks) > 0 || len(tb.MCPCalls) > 0 {
		result.Errors = append(result.Errors, runMCPAssertions(tb, plan, entities)...)
	}

	result.Passed = len(result.Errors) == 0
	result.Duration = time.Since(start)
	talonlog.BlockEval(context.Background(), tb.WhenBlock, tb.WhenKind, len(flagged), result.Duration)

	// Fire any per-row logger statements declared on the evaluated
	// block. Same code path the explain Tier-1 pipeline uses, so
	// `talon test` and `talon explain` produce identical logger
	// output for the same source.
	if b, ok := progBlocks[tb.WhenBlock]; ok {
		FireBlockLoggers(b, flagged, entities, time.Now().UTC())
	}
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

// evalQueryInMemory runs the structured query against the entity map by
// projecting entities into a MemoryStore and pulling the entity-IDs out of
// the resulting rows. The MemoryStore is the canonical in-memory backend;
// the testrunner uses it instead of its own evaluator so the two stay in
// lockstep (and the abstraction has a real second implementer).
func evalQueryInMemory(q factstore.Query, entities map[int]*entity) []int {
	store := storeFromEntities(entities)
	rows, err := store.Query(context.Background(), q)
	if err != nil {
		return nil
	}
	out := make([]int, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		if id, ok := toEntityID(row[0]); ok {
			out = append(out, id)
		}
	}
	return out
}

// storeFromEntities materialises the testrunner's entity map as a
// MemoryStore. The two structures are isomorphic; this is essentially a
// type cast that runs Assert under the hood so the resulting store is
// usable for both Query and (in REPL contexts) further mutation.
func storeFromEntities(entities map[int]*entity) *factstore.MemoryStore {
	s := factstore.NewMemoryStore()
	var facts []factstore.Fact
	for id, ent := range entities {
		idStr := strconv.Itoa(id)
		for attr, val := range ent.fields {
			facts = append(facts, factstore.Fact{
				RecordID:  idStr,
				Attribute: attr,
				Value:     val,
			})
		}
	}
	_ = s.Assert(context.Background(), facts)
	return s
}

func toEntityID(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}

// applyFilterConditions enforces the planner's `goConditions` against
// each flagged row. These are conditions the structured FactStore
// Query couldn't express — typically arithmetic (`attr "km" > attr
// "last_service_km" + 20000`) or other forms that need a per-row
// expression evaluator. Reuses internal/constraints.EvalCondition
// (originally built for #23 constraint blocks) so there's one code
// path for "per-row predicates over an entity's attrs."
//
// A row passes when every condition evaluates true. Evaluator errors
// (unknown expression shape, missing attr, divide-by-zero) keep the
// row, so a new clause type can't silently empty a result set —
// failing open beats failing closed for the testrunner's audit role.
func applyFilterConditions(conds []ast.Condition, flagged []int, entities map[int]*entity) []int {
	if len(conds) == 0 {
		return flagged
	}
	out := make([]int, 0, len(flagged))
	for _, id := range flagged {
		e := entities[id]
		if e == nil {
			continue
		}
		row := entityAttrsFlattened(e)
		keep := true
		for _, c := range conds {
			ok, err := constraints.EvalCondition(c, row)
			if err != nil {
				// Unsupported / unresolvable — fall back to keeping the
				// row. The block_eval log surfaces the row count so a
				// silent miss is still observable.
				continue
			}
			if !ok {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, id)
		}
	}
	return out
}

// The Datalog string parser that used to live here has been replaced by
// factstore.MemoryStore — see evalQueryInMemory above. The clause-walking
// logic lives in internal/factstore/memory.go now, and any backend with
// an in-process evaluator shares the same code path.

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

	// Two row shapes. Single-attribute primitives (anomaly, threshold)
	// read row[1] for the value. Multi-attribute primitives
	// (similarity_cosine, cluster_dbscan, forecast) self-serve attrs
	// from Input.Entities. We populate both unconditionally so
	// primitives don't have to negotiate.
	attr, _ := s.Params["attr"].(string)
	attrKey := ":attr/" + attr

	rows := make([][]any, 0, len(flagged))
	entitiesByID := make(map[int]map[string]any, len(flagged))
	for _, id := range flagged {
		e := entities[id]
		if e == nil {
			continue
		}
		if attr != "" {
			if val, ok := e.fields[attrKey]; ok {
				rows = append(rows, []any{id, val})
			}
		} else {
			rows = append(rows, []any{id})
		}
		entitiesByID[id] = entityAttrsFlattened(e)
	}

	prim, _ := reg.Get(s.Function)
	results, err := prim.Compute(context.Background(), mlruntime.Input{
		Rows:     rows,
		Schema:   map[string]int{"entity_id": 0, "value": 1},
		Params:   s.Params,
		Entities: entitiesByID,
	})
	if err != nil {
		// Sample too small — leave flagged unchanged so tests can still run
		// against tiny fixtures without forcing a synthetic 12-week window.
		return flagged, nil
	}

	keep := map[int]bool{}
	hasBoolResult := false
	explanations := make([]mlruntime.Explanation, 0, len(results))
	for _, r := range results {
		explanations = append(explanations, r.Explanation)
		switch v := r.Value.(type) {
		case bool:
			hasBoolResult = true
			if v {
				keep[r.EntityID] = true
			}
		default:
			// Non-bool result (cluster ID, days_until, similarity score)
			// — the primitive is producing information, not filtering.
			// Keep the row; let downstream filters (e.g. a `when` clause)
			// narrow.
			_ = v
			keep[r.EntityID] = true
		}
	}
	if !hasBoolResult {
		// No filtering primitive participated — return flagged unchanged
		// so the row set isn't accidentally reordered by the keep map's
		// iteration order.
		return flagged, explanations
	}
	out := make([]int, 0, len(flagged))
	for _, id := range flagged {
		if keep[id] {
			out = append(out, id)
		}
	}
	return out, explanations
}

// entityAttrsFlattened returns the entity's attribute map keyed by bare
// names (without the `:record/` or `:attr/` namespace), matching the
// shape multi-attribute ML primitives expect to query.
func entityAttrsFlattened(e *entity) map[string]any {
	out := make(map[string]any, len(e.fields))
	for k, v := range e.fields {
		switch {
		case strings.HasPrefix(k, ":record/"):
			out[strings.TrimPrefix(k, ":record/")] = v
		case strings.HasPrefix(k, ":attr/"):
			out[strings.TrimPrefix(k, ":attr/")] = v
		}
	}
	return out
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

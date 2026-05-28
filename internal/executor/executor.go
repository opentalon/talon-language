package executor

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/mlruntime"
	"github.com/opentalon/talon-language/internal/planner"
)

// FactStore is what the executor needs from a backing knowledge
// store: read facts via Query, write facts via Transact, declare a
// schema via Schema. Today the only shipped backend is Datalevin
// (internal/datalevin/client.go), and the concrete *datalevin.Client
// satisfies this interface unchanged. The interface name is
// intentionally backend-neutral so a future SQL- or vector-store-
// backed implementation can plug in without touching every call site.
//
// Method shapes do still reflect Datalog conventions (Query takes a
// query string, Transact takes a list of fact maps). When a non-
// Datalog backend lands, that backend's FactStore impl is responsible
// for translating the planner's Datalog into its native dialect; the
// interface itself is the point we'd refactor against.
//
// Health is intentionally absent — only the CLI calls it (cmd/talon/
// main.go), on the concrete client, before constructing the executor.
// Keeping it out of the FactStore interface lets fakes implement just
// what the executor exercises.
type FactStore interface {
	Query(ctx context.Context, query string) ([][]any, error)
	Transact(ctx context.Context, txData []map[string]any) error
	Schema(ctx context.Context, attrs map[string]map[string]string) error
}

// BlockResult is the outcome of executing one block's query plan.
type BlockResult struct {
	BlockName string
	Flagged   [][]any          // entities matched by the first DatalevinQuery
	Vars      map[string]any   // all intermediate result variables
	Steps     []StepResult     // per-step results for tracing
}

// StepResult records one step's execution.
type StepResult struct {
	Type   string // "DatalevinQuery", "GoComputation", "Filter"
	Name   string // function name or query variable
	Output any
}

// Executor runs compiled QueryPlans against a FactStore backend.
type Executor struct {
	Client      FactStore
	Registry    *mlruntime.Registry
	MCP         MCPCaller
	ConfirmHook ConfirmationHook
}

// NewExecutor creates an executor backed by the given FactStore and
// a registry pre-populated with the default ML primitives.
func NewExecutor(client FactStore) *Executor {
	return &Executor{Client: client, Registry: mlruntime.NewRegistry()}
}

// RunAll executes all plans and returns results keyed by block name.
func (e *Executor) RunAll(ctx context.Context, plans map[string]*planner.QueryPlan) (map[string]*BlockResult, error) {
	results := make(map[string]*BlockResult, len(plans))
	for name, plan := range plans {
		result, err := e.Run(ctx, plan)
		if err != nil {
			return results, fmt.Errorf("block %q: %w", name, err)
		}
		results[name] = result
	}
	return results, nil
}

// Run executes a single block's query plan.
func (e *Executor) Run(ctx context.Context, plan *planner.QueryPlan) (*BlockResult, error) {
	result := &BlockResult{
		BlockName: plan.BlockName,
		Vars:      map[string]any{},
	}

	for _, step := range plan.Steps {
		sr, err := e.execStep(ctx, step, result.Vars)
		if err != nil {
			return result, err
		}
		result.Steps = append(result.Steps, sr)
	}

	result.Flagged = flaggedRows(plan, result.Vars)
	return result, nil
}

// flaggedRows derives the block's flagged set. Start with the first
// DatalevinQuery's rows, then narrow to entities marked Value=true by any
// MLComputation step downstream.
func flaggedRows(plan *planner.QueryPlan, vars map[string]any) [][]any {
	var rows [][]any
	for _, step := range plan.Steps {
		if dq, ok := step.(*planner.DatalevinQuery); ok {
			if arr, ok := vars[dq.Into].([][]any); ok {
				rows = arr
			}
			break
		}
	}
	if rows == nil {
		return nil
	}

	for _, step := range plan.Steps {
		ml, ok := step.(*planner.MLComputation)
		if !ok {
			continue
		}
		flagged, ok := extractFlaggedIDs(vars[ml.Into])
		if !ok {
			continue
		}
		rows = filterRowsByID(rows, flagged)
	}
	return rows
}

// extractFlaggedIDs reads the entity IDs from an ML step's output where
// Value is the bool true.
func extractFlaggedIDs(out any) (map[int]bool, bool) {
	m, ok := out.(map[string]any)
	if !ok {
		return nil, false
	}
	rs, ok := m["results"].([]mlruntime.Result)
	if !ok {
		return nil, false
	}
	ids := map[int]bool{}
	for _, r := range rs {
		if v, _ := r.Value.(bool); v {
			ids[r.EntityID] = true
		}
	}
	return ids, true
}

// filterRowsByID keeps only rows whose first column (entity ID) is in keep.
func filterRowsByID(rows [][]any, keep map[int]bool) [][]any {
	out := make([][]any, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		id, ok := toInt(row[0])
		if !ok {
			continue
		}
		if keep[id] {
			out = append(out, row)
		}
	}
	return out
}

func (e *Executor) execStep(ctx context.Context, step planner.PlanStep, vars map[string]any) (StepResult, error) {
	switch s := step.(type) {
	case *planner.DatalevinQuery:
		return e.execQuery(ctx, s, vars)
	case *planner.GoComputation:
		return e.execComputation(ctx, s, vars)
	case *planner.MLComputation:
		return e.execMLComputation(s, vars)
	case *planner.Filter:
		return e.execFilter(s, vars)
	default:
		return StepResult{}, fmt.Errorf("unknown step type: %T", step)
	}
}

func (e *Executor) execQuery(ctx context.Context, dq *planner.DatalevinQuery, vars map[string]any) (StepResult, error) {
	// Skip empty queries (e.g. calculate clauses with no conditions)
	if strings.Contains(dq.Query, ":where]") || strings.HasSuffix(strings.TrimSpace(dq.Query), ":where]") {
		vars[dq.Into] = [][]any{}
		return StepResult{Type: "DatalevinQuery", Name: dq.Into, Output: [][]any{}}, nil
	}
	rows, err := e.Client.Query(ctx, dq.Query)
	if err != nil {
		return StepResult{}, fmt.Errorf("query into %q: %w", dq.Into, err)
	}
	vars[dq.Into] = rows
	return StepResult{
		Type:   "DatalevinQuery",
		Name:   dq.Into,
		Output: rows,
	}, nil
}

func (e *Executor) execComputation(ctx context.Context, gc *planner.GoComputation, vars map[string]any) (StepResult, error) {
	input := vars[gc.Input]

	switch gc.Function {
	case planner.FuncRenderTemplate:
		vars[gc.Into] = map[string]any{
			"input":    input,
			"template": gc.Params["template"],
		}
	case "resolve_block_matches":
		vars[gc.Into] = input
	case "mcp_call":
		result, err := e.execMCPCall(ctx, gc, vars)
		if err != nil {
			return StepResult{}, err
		}
		vars[gc.Into] = result
	default:
		vars[gc.Into] = map[string]any{
			"function": gc.Function,
			"input":    input,
			"params":   gc.Params,
			"status":   "stub",
		}
	}

	return StepResult{
		Type:   "GoComputation",
		Name:   gc.Function,
		Output: vars[gc.Into],
	}, nil
}

func (e *Executor) execMCPCall(ctx context.Context, gc *planner.GoComputation, vars map[string]any) (any, error) {
	mcpCall, ok := gc.Params["mcp"].(*ast.MCPCall)
	if !ok {
		return map[string]any{"status": "stub", "step": gc.Params["step"]}, nil
	}

	if e.MCP == nil {
		return map[string]any{"status": "stub", "step": gc.Params["step"]}, nil
	}

	stepName, _ := gc.Params["step"].(string)

	// Confirmation hook
	if e.ConfirmHook != nil {
		proceed, err := e.ConfirmHook(ctx, stepName, mcpCall.Server, mcpCall.Tool)
		if err != nil {
			return nil, fmt.Errorf("step %q: confirmation: %w", stepName, err)
		}
		if !proceed {
			return map[string]any{"status": "skipped", "reason": "confirmation_denied"}, nil
		}
	}

	// Resolve MCP args
	args := resolveMCPArgs(mcpCall.Args, vars)

	// Check for collect_all
	collectAll := false
	if ca, ok := args["collect_all"]; ok {
		if b, ok := ca.(bool); ok && b {
			collectAll = true
		}
		delete(args, "collect_all")
	}

	if !collectAll {
		return e.MCP.Call(ctx, mcpCall.Server, mcpCall.Tool, args)
	}

	// Auto-paginate: call repeatedly until has_more is false
	return e.collectAll(ctx, mcpCall.Server, mcpCall.Tool, args)
}

func (e *Executor) collectAll(ctx context.Context, server, tool string, args map[string]any) (any, error) {
	var allItems []any
	page := 1
	for {
		args["page"] = page
		result, err := e.MCP.Call(ctx, server, tool, args)
		if err != nil {
			return nil, err
		}
		m, ok := result.(map[string]any)
		if !ok {
			return result, nil
		}
		if items, ok := m["items"].([]any); ok {
			allItems = append(allItems, items...)
		}
		hasMore, _ := m["has_more"].(bool)
		if !hasMore {
			break
		}
		page++
	}
	return map[string]any{"items": allItems}, nil
}

// resolveMCPArgs evaluates each MCP argument expression against step results.
func resolveMCPArgs(exprArgs map[string]ast.Expr, vars map[string]any) map[string]any {
	args := map[string]any{}
	for k, expr := range exprArgs {
		args[k] = resolveExprValue(expr, vars)
	}
	return args
}

func resolveExprValue(expr ast.Expr, vars map[string]any) any {
	switch e := expr.(type) {
	case *ast.LiteralExpr:
		return e.Value
	case *ast.StepResultExpr:
		return resolveStepField(vars, e.StepName, e.Field)
	case *ast.MapExpr:
		src := resolveExprValue(e.Source, vars)
		return resolveMap(src, e.Field)
	case *ast.ContextExpr:
		return resolveStepField(vars, "context", e.Field)
	case *ast.IdentExpr:
		return e.Name
	default:
		return nil
	}
}

// resolveStepField navigates step("name").result.field by traversing the
// stored step result using dot-separated field paths.
func resolveStepField(vars map[string]any, stepName, field string) any {
	result := vars[stepName+"_result"]
	if result == nil {
		return nil
	}
	for _, part := range strings.Split(field, ".") {
		m, ok := result.(map[string]any)
		if !ok {
			return nil
		}
		result = m[part]
	}
	return result
}

// resolveMap extracts a field from each element of an array.
// e.g. items.map(id) → [item1["id"], item2["id"], ...]
func resolveMap(src any, field string) any {
	arr, ok := src.([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(arr))
	for _, elem := range arr {
		m, ok := elem.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, m[field])
	}
	return out
}

// execMLComputation dispatches an MLComputation step to the registry.
// Primitives without a registered implementation fall back to a structured
// stub so downstream steps (render_template, filters) can still run.
func (e *Executor) execMLComputation(ml *planner.MLComputation, vars map[string]any) (StepResult, error) {
	input := vars[ml.Input]

	if e.Registry != nil && e.Registry.Has(ml.Function) {
		prim, _ := e.Registry.Get(ml.Function)
		rows, _ := input.([][]any)
		results, err := prim.Compute(context.Background(), mlruntime.Input{
			Rows:   rows,
			Schema: schemaFromParams(ml.Params),
			Params: ml.Params,
		})
		if err != nil {
			return StepResult{}, fmt.Errorf("ml %s: %w", ml.Function, err)
		}
		vars[ml.Into] = map[string]any{
			"function": ml.Function,
			"results":  results,
		}
		return StepResult{
			Type:   "MLComputation",
			Name:   ml.Function,
			Output: vars[ml.Into],
		}, nil
	}

	vars[ml.Into] = map[string]any{
		"function":     ml.Function,
		"input":        input,
		"params":       ml.Params,
		"status":       "stub",
		"explanations": []any{},
	}
	return StepResult{
		Type:   "MLComputation",
		Name:   ml.Function,
		Output: vars[ml.Into],
	}, nil
}

func (e *Executor) execFilter(f *planner.Filter, vars map[string]any) (StepResult, error) {
	// Filters contain Go predicate expressions that need a proper evaluator.
	// For now, pass through the input unfiltered.
	vars[f.Into] = vars[f.Input]
	return StepResult{
		Type:   "Filter",
		Name:   f.Condition,
		Output: vars[f.Into],
	}, nil
}

// Seed reads given blocks from a parsed .talon.test program, infers the schema,
// and pushes data to Datalevin via the HTTP client.
func (e *Executor) Seed(ctx context.Context, prog *ast.Program) (int, error) {
	// Collect all given data across all test blocks, dedup by entity ID
	entities := map[int]map[string]any{}
	for _, b := range prog.Blocks {
		tb, ok := b.(*ast.TestBlock)
		if !ok {
			continue
		}
		for _, d := range tb.Given {
			ent, exists := entities[d.ID]
			if !exists {
				ent = map[string]any{}
				entities[d.ID] = ent
			}
			for k, v := range d.Fields {
				attr := fieldNamespace(d.Kind, k)
				ent[attr] = v
			}
		}
	}

	if len(entities) == 0 {
		return 0, nil
	}

	// Infer schema from entity values
	schema := map[string]map[string]string{}
	for _, fields := range entities {
		for attr, val := range fields {
			if _, exists := schema[attr]; exists {
				continue
			}
			schema[attr] = map[string]string{"db/valueType": inferType(val)}
		}
	}

	// Push schema
	if err := e.Client.Schema(ctx, schema); err != nil {
		return 0, fmt.Errorf("seed schema: %w", err)
	}

	// Build transaction data
	var txData []map[string]any
	for _, fields := range entities {
		tx := map[string]any{}
		for attr, val := range fields {
			tx[":"+attr[1:]] = val // ":record/type" → ":record/type" (already prefixed)
		}
		txData = append(txData, tx)
	}

	if err := e.Client.Transact(ctx, txData); err != nil {
		return 0, fmt.Errorf("seed transact: %w", err)
	}

	return len(entities), nil
}

// ResolveNames looks up :attr/name for all entities.
func (e *Executor) ResolveNames(ctx context.Context, _ []int) (map[int]string, error) {
	rows, err := e.Client.Query(ctx, `[:find ?e ?name :where [?e :attr/name ?name]]`)
	if err != nil {
		return nil, err
	}
	names := map[int]string{}
	for _, row := range rows {
		if len(row) >= 2 {
			if eid, ok := toInt(row[0]); ok {
				if name, ok := row[1].(string); ok {
					names[eid] = name
				}
			}
		}
	}
	return names, nil
}

// schemaFromParams extracts column indices the planner stuffed into Params
// (value_index, entity_id_index) so primitives can read the right cells.
func schemaFromParams(params map[string]any) map[string]int {
	if params == nil {
		return nil
	}
	schema := map[string]int{}
	if idx, ok := intParam(params, "value_index"); ok && idx >= 0 {
		schema["value"] = idx
	}
	if idx, ok := intParam(params, "entity_id_index"); ok && idx >= 0 {
		schema["entity_id"] = idx
	}
	if len(schema) == 0 {
		return nil
	}
	return schema
}

func intParam(params map[string]any, key string) (int, bool) {
	switch v := params[key].(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	}
	return 0, false
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
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

func inferType(val any) string {
	switch val.(type) {
	case float64:
		return "db.type/long"
	case string:
		return "db.type/string"
	case bool:
		return "db.type/boolean"
	default:
		return "db.type/string"
	}
}

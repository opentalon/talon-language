package executor

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/datalevin"
	"github.com/opentalon/talon-language/internal/planner"
)

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

// Executor runs compiled QueryPlans against a Datalevin server.
type Executor struct {
	Client *datalevin.Client
}

// NewExecutor creates an executor backed by the given Datalevin client.
func NewExecutor(client *datalevin.Client) *Executor {
	return &Executor{Client: client}
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

	// The first DatalevinQuery result is the "flagged" set
	for _, step := range plan.Steps {
		if dq, ok := step.(*planner.DatalevinQuery); ok {
			if rows, ok := result.Vars[dq.Into]; ok {
				if arr, ok := rows.([][]any); ok {
					result.Flagged = arr
				}
			}
			break
		}
	}

	return result, nil
}

func (e *Executor) execStep(ctx context.Context, step planner.PlanStep, vars map[string]any) (StepResult, error) {
	switch s := step.(type) {
	case *planner.DatalevinQuery:
		return e.execQuery(ctx, s, vars)
	case *planner.GoComputation:
		return e.execComputation(s, vars)
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

func (e *Executor) execComputation(gc *planner.GoComputation, vars map[string]any) (StepResult, error) {
	input := vars[gc.Input]

	switch gc.Function {
	case planner.FuncRenderTemplate:
		// Template rendering: pass through input with template metadata
		vars[gc.Into] = map[string]any{
			"input":    input,
			"template": gc.Params["template"],
		}
	case "resolve_block_matches":
		// Pass through — the referenced block's results would be resolved at runtime
		vars[gc.Into] = input
	case "mcp_call":
		// MCP calls are stubbed — would call external tool servers
		vars[gc.Into] = map[string]any{"status": "stub", "step": gc.Params["step"]}
	default:
		// ML functions: anomaly_zscore, predict_decision_tree, etc.
		// Stub: pass through input with function metadata
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

package executor

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/factstore"
	"github.com/opentalon/talon-language/internal/planner"
)

// ErrNoVectorBackend is returned when a plan contains a
// VectorSimilarStep but the executor's VectorBackend is nil. Callers
// should wire a talon-db adapter (or compatible) before running a
// `find similar ... using vector` block.
var ErrNoVectorBackend = errors.New("executor: find similar using vector requires a VectorBackend (talon-db adapter)")

// execVectorSimilar resolves the `to` expression to a seed entity id,
// reads the seed's vector from the FactStore under `:vector/<scope>`,
// asks the VectorBackend for the top-K nearest neighbours, and keeps
// the candidate rows whose entity-id appears among the hits. Hits
// outside Within are dropped when Within is set.
//
// The seed entity itself is filtered from the result — `find similar`
// is about neighbours, not the seed.
func (e *Executor) execVectorSimilar(ctx context.Context, s *planner.VectorSimilarStep, vars map[string]any) (StepResult, error) {
	if e.VectorBackend == nil {
		return StepResult{}, ErrNoVectorBackend
	}
	rows, _ := vars[s.Input].([][]any)
	if len(rows) == 0 || s.To == nil {
		vars[s.Into] = rows
		return StepResult{Type: "VectorSimilarStep", Name: s.BlockName, Output: rows}, nil
	}

	seedID, ok := resolveSeedEntityID(s.To)
	if !ok {
		return StepResult{}, fmt.Errorf("find similar: cannot resolve seed entity from %T", s.To)
	}
	seedVec, err := e.readSeedVector(ctx, seedID, s.Scope)
	if err != nil {
		return StepResult{}, fmt.Errorf("find similar: read seed vector: %w", err)
	}

	hits, err := e.VectorBackend.VectorSearch(ctx, s.Scope, seedVec, s.TopK+1)
	if err != nil {
		return StepResult{}, fmt.Errorf("find similar: VectorSearch: %w", err)
	}
	keep := make(map[string]bool, len(hits))
	for _, h := range hits {
		if h.ID == seedID {
			continue
		}
		if s.Within != nil && float64(h.Distance) > *s.Within {
			continue
		}
		keep[h.ID] = true
		if len(keep) == s.TopK {
			break
		}
	}

	out := make([][]any, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		id := toStringID(row[0])
		if keep[id] {
			out = append(out, row)
		}
	}
	vars[s.Into] = out
	return StepResult{Type: "VectorSimilarStep", Name: s.BlockName, Output: out}, nil
}

// readSeedVector pulls `:vector/<scope>` for the seed entity from the
// FactStore. Vector facts are stored as []float64 (the JSON-native
// numeric type the rest of the FactStore uses); we cast to float32 on
// read because that's what the HNSW wire surface accepts.
func (e *Executor) readSeedVector(ctx context.Context, seedID, scope string) ([]float32, error) {
	attr := ":vector/" + scope
	rows, err := e.Client.Query(ctx, factstore.Query{
		Find: []string{"?v"},
		Where: []factstore.Clause{
			&factstore.Pattern{
				Entity:    factstore.Lit(seedID),
				Attribute: attr,
				Value:     factstore.Var("v"),
			},
		},
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return nil, fmt.Errorf("seed %q has no %s fact", seedID, attr)
	}
	raw := rows[0][0]
	switch v := raw.(type) {
	case []float32:
		return append([]float32(nil), v...), nil
	case []float64:
		out := make([]float32, len(v))
		for i, f := range v {
			out[i] = float32(f)
		}
		return out, nil
	case []any:
		out := make([]float32, len(v))
		for i, x := range v {
			switch f := x.(type) {
			case float64:
				out[i] = float32(f)
			case float32:
				out[i] = f
			case int:
				out[i] = float32(f)
			case int64:
				out[i] = float32(f)
			default:
				return nil, fmt.Errorf("seed vector element %d has type %T", i, x)
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("seed vector has type %T, want []float64 or []float32", raw)
}

// resolveSeedEntityID accepts the same expression shapes the rest of
// the executor handles for seed-style clauses: a literal int (`to 123`)
// or the `record(N)` constructor.
func resolveSeedEntityID(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.LiteralExpr:
		switch v := e.Value.(type) {
		case int:
			return strconv.Itoa(v), true
		case int64:
			return strconv.FormatInt(v, 10), true
		case float64:
			return strconv.FormatInt(int64(v), 10), true
		case string:
			return v, true
		}
	}
	return "", false
}

func toStringID(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	case float64:
		return strconv.FormatInt(int64(n), 10)
	}
	return fmt.Sprintf("%v", v)
}

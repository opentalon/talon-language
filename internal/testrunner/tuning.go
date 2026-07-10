package testrunner

import (
	"fmt"
	"math"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/explain"
	"github.com/opentalon/talon-language/internal/mlruntime"
	"github.com/opentalon/talon-language/internal/optimize"
	"github.com/opentalon/talon-language/internal/planner"
)

// tunableSpec describes how to tune one ML primitive's parameter via ABC:
// which planner function it binds to, which key in MLComputation.Params holds
// the parameter, the continuous search bounds, and an optional transform that
// converts ABC's float64 to the exact param shape the primitive expects.
//
// To make a new primitive tunable, add an entry here, ensure the primitive
// actually accepts the param (most do), then update the validator's
// `detectHasTunablePrimitive` to recognize the new shape.
type tunableSpec struct {
	Function string             // mlruntime function name (planner.FuncXxx)
	Param    string             // key in MLComputation.Params
	Bounds   optimize.ABCBounds // continuous search space
	// Encode transforms ABC's raw float64 into the value the primitive expects.
	// nil = pass through as float64. Used for integer-valued params (k in
	// k-NN) or string-formatted percentiles ("p<int>") in learned_threshold.
	Encode func(x float64) any
	// Display formats the encoded value for human-readable Decision evidence.
	// nil = use fmt %v on the encoded value.
	Display func(encoded any) string
}

// tunables is the per-primitive registry. v1 ships anomaly_zscore and
// learned_threshold. forecast/predict/cluster/classify/similar will become
// tunable the day their primitives ship — adding a tunableSpec entry is
// the only required change here.
var tunables = []tunableSpec{
	{
		Function: planner.FuncAnomalyZscore,
		Param:    "threshold",
		Bounds:   optimize.ABCBounds{Min: 0.5, Max: 4.0},
	},
	{
		Function: planner.FuncLearnedThreshold,
		Param:    "method",
		Bounds:   optimize.ABCBounds{Min: 50, Max: 99},
		Encode: func(x float64) any {
			n := int(math.Round(x))
			if n < 1 {
				n = 1
			}
			if n > 99 {
				n = 99
			}
			return fmt.Sprintf("p%d", n)
		},
		Display: func(v any) string {
			if s, ok := v.(string); ok {
				return s
			}
			return fmt.Sprintf("%v", v)
		},
	},
}

// tuningResult captures one block's auto-tuned parameter set and the labeled-
// fixture diagnostics that produced it. Surfaced into Decision evidence so
// `talon explain` can render the tuning provenance alongside the threshold
// itself.
type tuningResult struct {
	BlockName    string
	AgainstTest  string
	Function     string // which primitive was tuned (anomaly_zscore, learned_threshold)
	ParamName    string // which key in Params holds the tuned value
	ParamValue   any    // encoded value (string for learned_threshold, float64 for anomaly)
	DisplayValue string // human-readable rendering for explain output
	F1           float64
	Precision    float64
	Recall       float64
	SampleSize   int
	Iterations   int
}

// computeTunings walks every detect block in the program, finds those with a
// `tune against test "..."` clause, runs ABC on whatever tunable primitive
// the block contains, and returns a map keyed by detect block name. Blocks
// without a tune clause are absent.
func computeTunings(prog *ast.Program, plans map[string]*planner.QueryPlan) map[string]*tuningResult {
	out := map[string]*tuningResult{}
	for _, b := range prog.Blocks {
		det, ok := b.(*ast.DetectBlock)
		if !ok || det.Tune == nil {
			continue
		}
		labeled := findTestByName(prog, det.Tune.AgainstTest)
		if labeled == nil {
			continue
		}
		plan, ok := plans[det.Name]
		if !ok {
			continue
		}
		if res := tunePrimitiveAgainst(plan, labeled); res != nil {
			res.BlockName = det.Name
			res.AgainstTest = det.Tune.AgainstTest
			out[det.Name] = res
		}
	}
	return out
}

// tunePrimitiveAgainst finds the first tunable primitive in the plan, then
// runs ABC over its parameter space against the labeled fixture's F1.
// Returns nil if no tunable primitive is present.
func tunePrimitiveAgainst(plan *planner.QueryPlan, labeled *ast.TestBlock) *tuningResult {
	step, spec := findTunableStep(plan)
	if step == nil {
		return nil
	}

	entities := buildEntities(labeled.Given)
	labels := extractLabels(labeled)
	if len(labels) == 0 {
		return nil
	}
	actual := map[int]bool{}
	for id := range labels {
		actual[id] = true
	}

	reg := mlruntime.NewRegistry()
	candidateIDs := computeCandidateIDs(plan, entities)

	fitness := func(x []float64) float64 {
		value := encodeParam(spec, x[0])
		stepCopy := *step
		stepCopy.Params = cloneParams(step.Params)
		stepCopy.Params[spec.Param] = value
		predIDs, _ := narrowByML(reg, &stepCopy, candidateIDs, entities, nil)
		pred := map[int]bool{}
		for _, id := range predIDs {
			pred[id] = true
		}
		return optimize.BinaryF1(pred, actual)
	}

	res := optimize.ABC(fitness, []optimize.ABCBounds{spec.Bounds},
		optimize.ABCConfig{Seed: 42, Iterations: 40, ColonySize: 16})

	// Final pass at the tuned value to record P/R alongside F1.
	bestValue := encodeParam(spec, res.Best[0])
	stepCopy := *step
	stepCopy.Params = cloneParams(step.Params)
	stepCopy.Params[spec.Param] = bestValue
	predIDs, _ := narrowByML(reg, &stepCopy, candidateIDs, entities, nil)
	pred := map[int]bool{}
	for _, id := range predIDs {
		pred[id] = true
	}
	p, r := optimize.BinaryPrecisionRecall(pred, actual)

	return &tuningResult{
		Function:     spec.Function,
		ParamName:    spec.Param,
		ParamValue:   bestValue,
		DisplayValue: displayParam(spec, bestValue),
		F1:           res.Fitness,
		Precision:    p,
		Recall:       r,
		SampleSize:   len(entities),
		Iterations:   len(res.History),
	}
}

// findTunableStep returns the first MLComputation in the plan whose function
// has a registered tunableSpec, plus that spec. Returns (nil, nil) when no
// tunable step exists.
func findTunableStep(plan *planner.QueryPlan) (*planner.MLComputation, *tunableSpec) {
	for _, step := range plan.Steps {
		ml, ok := step.(*planner.MLComputation)
		if !ok {
			continue
		}
		for i := range tunables {
			if tunables[i].Function == ml.Function {
				return ml, &tunables[i]
			}
		}
	}
	return nil, nil
}

// encodeParam runs the tunableSpec's Encode transform if present, otherwise
// returns the raw float64. Used by both ABC's fitness evaluation and the
// final result extraction.
func encodeParam(spec *tunableSpec, x float64) any {
	if spec.Encode != nil {
		return spec.Encode(x)
	}
	// Round to 2 decimals so injected params don't carry float noise.
	return math.Round(x*100) / 100
}

// displayParam formats an encoded value for human-readable evidence. The
// tunableSpec can override; default is fmt %v.
func displayParam(spec *tunableSpec, v any) string {
	if spec.Display != nil {
		return spec.Display(v)
	}
	return fmt.Sprintf("%v", v)
}

func findTestByName(prog *ast.Program, name string) *ast.TestBlock {
	for _, b := range prog.Blocks {
		if tb, ok := b.(*ast.TestBlock); ok && tb.Name == name {
			return tb
		}
	}
	return nil
}

// computeCandidateIDs runs the plan's first FactQuery against the
// in-memory entities and returns the resulting entity IDs. This is the
// candidate population the ML primitive sees.
func computeCandidateIDs(plan *planner.QueryPlan, entities map[int]*entity) []int {
	for _, step := range plan.Steps {
		if dq, ok := step.(*planner.FactQuery); ok {
			return evalQueryInMemory(dq.Query, entities)
		}
	}
	// Fallback: all entities.
	ids := make([]int, 0, len(entities))
	for id := range entities {
		ids = append(ids, id)
	}
	return ids
}

// extractLabels reads `expect flagged ID` lines from a test block and returns
// the set of entity IDs labeled positive. Other entities in the test's
// `given` are treated as negatives under closed-world assumption.
func extractLabels(tb *ast.TestBlock) map[int]bool {
	out := map[int]bool{}
	for _, a := range tb.Expect {
		if a.Kind == "flagged" {
			out[a.ID] = true
		}
	}
	return out
}

// appendTuningWhy adds a one-liner attributing the tuned parameter to ABC
// against the named labeled fixture. Comes after the block's natural Why
// lines so the rule's logic remains the primary explanation.
func appendTuningWhy(why []string, tr *tuningResult) []string {
	return append(why, fmt.Sprintf(
		"%s auto-tuned via ABC against test %q — %s=%s, F1=%.2f (P=%.2f, R=%.2f) on %d samples",
		tr.ParamName, tr.AgainstTest, tr.ParamName, tr.DisplayValue,
		tr.F1, tr.Precision, tr.Recall, tr.SampleSize,
	))
}

// appendTuningEvidence records the tuning metadata as machine-readable facts
// so audits and JSON consumers see the exact tuned value, the fixture name,
// and the F1 that justified picking it.
func appendTuningEvidence(facts []explain.Fact, tr *tuningResult) []explain.Fact {
	return append(facts,
		explain.Fact{Attribute: "tuned_" + tr.ParamName, Value: tr.DisplayValue},
		explain.Fact{Attribute: "tuned_against", Value: tr.AgainstTest},
		explain.Fact{Attribute: "tuned_f1", Value: tr.F1},
		explain.Fact{Attribute: "tuned_precision", Value: tr.Precision},
		explain.Fact{Attribute: "tuned_recall", Value: tr.Recall},
	)
}

func cloneParams(p map[string]any) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

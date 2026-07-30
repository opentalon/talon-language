package planner

import (
	"testing"

	"github.com/opentalon/talon-language/internal/lexer"
	"github.com/opentalon/talon-language/internal/mlmodel"
	"github.com/opentalon/talon-language/internal/mlruntime"
	"github.com/opentalon/talon-language/internal/parser"
)

func classifyStep(t *testing.T, plan *QueryPlan) *MLComputation {
	t.Helper()
	for _, s := range plan.Steps {
		if ml, ok := s.(*MLComputation); ok && ml.Function == FuncClassifyKNN {
			return ml
		}
	}
	t.Fatalf("no classify_knn step in plan; steps: %+v", plan.Steps)
	return nil
}

// TestUsingModelTalon: a Talon `model` block supplies the classify training
// set inline, and no training FactQuery is emitted.
func TestUsingModelTalon(t *testing.T) {
	plans := planAll(t, `
model "failure_risk" {
  classify knn k 3
  features [attr "km", attr "age"]
  fitted {
    example [50000, 8] label "high"
    example [10000, 2] label "low"
  }
}
classify "Risk" {
  for records where type == "vehicle"
  using model "failure_risk"
}`)
	ml := classifyStep(t, plans["Risk"])
	rows, ok := ml.Params["fitted_rows"].([]mlruntime.TrainingRow)
	if !ok || len(rows) != 2 {
		t.Fatalf("expected 2 fitted rows from the model, got %+v", ml.Params["fitted_rows"])
	}
	if ml.Params["model_provider"] != "talon" || ml.Params["k"] != 3 {
		t.Fatalf("provider/k mismatch: %v / %v", ml.Params["model_provider"], ml.Params["k"])
	}
	names, _ := ml.Params["feature_names"].([]string)
	if len(names) != 2 || names[0] != "km" {
		t.Fatalf("feature names should come from the model, got %v", names)
	}
}

// TestUsingModelGo: a model registered in Go resolves through PlanWithModels
// under the same `using model` reference — the "Go module" provider.
func TestUsingModelGo(t *testing.T) {
	src := `classify "Risk" {
  for records where type == "vehicle"
  using model "vendor.ml.failure_risk"
}`
	tokens, _ := lexer.Lex("t.talon", src)
	prog, pd := parser.Parse("t.talon", tokens)
	if pd.HasErrors() {
		t.Fatalf("parse: %v", pd)
	}

	goReg := mlmodel.NewRegistry()
	goReg.Register("vendor.ml.failure_risk", &mlmodel.Model{
		Name: "failure_risk", Algo: "classify_knn", K: 5,
		Features: []string{"km", "age"},
		Examples: []mlmodel.Example{
			{Features: map[string]float64{"km": 50000, "age": 8}, Label: "high"},
			{Features: map[string]float64{"km": 10000, "age": 2}, Label: "low"},
		},
	})

	plans, diags := PlanWithModels(prog, goReg)
	if diags.HasErrors() {
		t.Fatalf("plan: %v", diags)
	}
	ml := classifyStep(t, plans["Risk"])
	if ml.Params["model_provider"] != "go" {
		t.Fatalf("expected go provider, got %v", ml.Params["model_provider"])
	}
	rows, ok := ml.Params["fitted_rows"].([]mlruntime.TrainingRow)
	if !ok || len(rows) != 2 {
		t.Fatalf("expected 2 fitted rows from the Go model, got %+v", ml.Params["fitted_rows"])
	}
}

// TestUsingUnknownModelErrors: referencing a model neither provider knows is a
// plan-time error.
func TestUsingUnknownModelErrors(t *testing.T) {
	src := `classify "Risk" {
  for records where type == "vehicle"
  using model "nope.nope"
}`
	tokens, _ := lexer.Lex("t.talon", src)
	prog, _ := parser.Parse("t.talon", tokens)
	_, diags := Plan(prog)
	if !diags.HasErrors() {
		t.Fatal("expected an error for an unknown model reference")
	}
}

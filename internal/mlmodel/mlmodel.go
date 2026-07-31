// Package mlmodel is the shared representation and resolver for named ML
// models (issue #13 ML-module system). A model carries inline fitted params;
// a classify/predict block references it via `using model "<qualified name>"`.
//
// Models come from two providers, resolved by the same qualified name:
//
//   - Talon modules — `model` blocks parsed from source (optionally namespaced
//     by an enclosing `module "ns" { export ... }`), converted via FromAST.
//   - Go modules — models a host registers directly in a Registry.
//
// A Resolver consults the Talon models first, then the Go registry, so both
// kinds are usable interchangeably.
package mlmodel

import (
	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/mlruntime"
)

// Example is one labeled point in a model's inline fitted set.
type Example struct {
	Features map[string]float64
	Label    string
}

// Model is the resolved, provider-agnostic form of an ML model. For kNN
// (lazy) Examples IS the fitted state; for a decision tree (eager) Tree holds
// the fitted splits + leaves.
type Model struct {
	Name     string
	Algo     string   // "classify_knn" | "predict_decision_tree"
	K        int      // neighbours, for kNN
	Features []string // ordered feature attribute names
	Examples []Example
	Tree     []mlruntime.FittedTreeNode
}

// TrainingRows materialises the fitted examples as mlruntime training rows,
// the form the classify_knn primitive consumes. Synthetic negative ids keep
// them distinct from real candidate entities.
func (m *Model) TrainingRows() []mlruntime.TrainingRow {
	rows := make([]mlruntime.TrainingRow, 0, len(m.Examples))
	for i, ex := range m.Examples {
		attrs := make(map[string]any, len(ex.Features))
		for k, v := range ex.Features {
			attrs[k] = v
		}
		rows = append(rows, mlruntime.TrainingRow{ID: -(i + 1), Attrs: attrs, Label: ex.Label})
	}
	return rows
}

// FromAST converts a parsed model block into a resolved Model. featureName
// maps each feature expression to its bare attribute name (a small helper the
// caller supplies so this package needn't depend on expression rendering).
func FromAST(b *ast.ModelBlock, featureName func(ast.Expr) string) *Model {
	feats := make([]string, 0, len(b.Features))
	for _, f := range b.Features {
		feats = append(feats, featureName(f))
	}
	examples := make([]Example, 0, len(b.Examples))
	for _, ex := range b.Examples {
		fm := make(map[string]float64, len(feats))
		for i, name := range feats {
			if i < len(ex.Features) {
				fm[name] = ex.Features[i]
			}
		}
		examples = append(examples, Example{Features: fm, Label: ex.Label})
	}
	tree := make([]mlruntime.FittedTreeNode, 0, len(b.Tree))
	for _, n := range b.Tree {
		tree = append(tree, mlruntime.FittedTreeNode{
			Index: n.Index, Leaf: n.Leaf, Class: n.Class, Purity: n.Purity,
			Feature: n.Feature, Threshold: n.Threshold, Left: n.Left, Right: n.Right,
		})
	}
	return &Model{Name: b.Name, Algo: b.Algo, K: b.K, Features: feats, Examples: examples, Tree: tree}
}

// Registry holds Go-provided models keyed by qualified name — the "Go module"
// provider. A host registers models it built in Go so Talon source can
// reference them exactly like Talon-authored ones.
type Registry struct {
	models map[string]*Model
}

// NewRegistry returns an empty Go-model registry.
func NewRegistry() *Registry { return &Registry{models: map[string]*Model{}} }

// Register adds or replaces a model under its qualified name.
func (r *Registry) Register(qualifiedName string, m *Model) {
	if r.models == nil {
		r.models = map[string]*Model{}
	}
	r.models[qualifiedName] = m
}

// Get returns the model registered under name, if any.
func (r *Registry) Get(name string) (*Model, bool) {
	m, ok := r.models[name]
	return m, ok
}

// Resolver resolves a qualified model name against Talon models first, then
// the Go registry — so `using model "x"` finds either provider.
type Resolver struct {
	talon map[string]*Model // qualified name → Talon-authored model
	goReg *Registry         // host-registered Go models (may be nil)
}

// NewResolver builds a resolver over the Talon models (qualified name → model)
// and an optional Go registry.
func NewResolver(talon map[string]*Model, goReg *Registry) *Resolver {
	return &Resolver{talon: talon, goReg: goReg}
}

// Resolve returns the model bound to a qualified name and which provider
// supplied it ("talon" or "go"), or ok=false if neither has it.
func (r *Resolver) Resolve(name string) (m *Model, provider string, ok bool) {
	if r != nil && r.talon != nil {
		if m, ok := r.talon[name]; ok {
			return m, "talon", true
		}
	}
	if r != nil && r.goReg != nil {
		if m, ok := r.goReg.Get(name); ok {
			return m, "go", true
		}
	}
	return nil, "", false
}

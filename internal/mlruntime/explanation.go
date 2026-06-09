package mlruntime

import "context"

// Explanation is the auditable trace of one ML primitive invocation.
// Serialised to JSON for `talon trace` and audit logs. See ADR-0001.
type Explanation struct {
	Primitive  string         `json:"primitive"`
	EntityID   int            `json:"entity_id"`
	Inputs     map[string]any `json:"inputs,omitempty"`
	Rules      []Rule         `json:"rules,omitempty"`
	Confidence float64        `json:"confidence,omitempty"`
	Threshold  *Threshold     `json:"threshold,omitempty"`
}

// Rule is one human-readable predicate that fired during the decision.
// "operating_hours > 2000", "z_score > 2.5", "matches_cluster #3".
type Rule struct {
	Attr     string `json:"attr"`
	Op       string `json:"op"`
	Value    any    `json:"value"`
	Observed any    `json:"observed"`
}

// Threshold records the method and value behind a learned threshold or
// anomaly bound — the sample size used is kept so reviewers can judge
// the confidence behind the cut-off.
type Threshold struct {
	Method string  `json:"method"`
	Value  float64 `json:"value"`
	Sample int     `json:"sample"`
}

// Primitive is implemented once per ML keyword.
type Primitive interface {
	Name() string
	Compute(ctx context.Context, in Input) ([]Result, error)
}

// Input is the data and parameters handed to a Primitive.
// Rows come from the upstream FactQuery; Schema maps column names to
// row indices; Params carries the planner's MLComputation.Params verbatim.
//
// Entities is the optional full-attribute view of each candidate entity.
// Single-attribute primitives (z-score anomaly, learned threshold) use
// Rows + Schema. Multi-attribute primitives (cosine similarity, DBSCAN,
// classify, predict) read attributes directly from Entities so they
// don't have to pre-coordinate column ordering with the dispatcher.
//
// Keys in the inner map are bare attribute names — without the
// `:record/` or `:attr/` namespace prefix the FactStore uses internally
// — matching the form a Talon source file references via `attr "name"`.
type Input struct {
	Rows     [][]any
	Schema   map[string]int
	Params   map[string]any
	Entities map[int]map[string]any
}

// Result is one entity's prediction + its explanation.
type Result struct {
	EntityID    int
	Value       any
	Explanation Explanation
}

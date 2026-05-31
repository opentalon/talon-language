package optimize

// Direction picks whether an objective is minimized or maximized.
type Direction int

const (
	Minimize Direction = iota
	Maximize
)

func (d Direction) String() string {
	if d == Maximize {
		return "maximize"
	}
	return "minimize"
}

// Objective names one optimization dimension. Order in the Objective slice
// passed to Pareto matches the order of Individual.Values.
type Objective struct {
	Name string
	Dir  Direction
}

// Individual is one candidate evaluated against every objective. Row is an
// opaque pass-through — v1 holds the original Datalevin row ([]any); v2's
// SubsetProblem holds a binary mask ([]bool). Callers cast back as needed.
type Individual struct {
	EntityID int
	Values   []float64 // parallel to []Objective
	Row      any
}

// Solution is the ranked output for a single Individual.
type Solution struct {
	EntityID       int
	Rank           int     // 0 == Pareto frontier
	CrowdingDist   float64 // +Inf for boundary points
	DominatedCount int     // |{j : j ≺ i}|
	Dominates      int     // |{j : i ≺ j}|
	Values         []float64
	Row            any
}

// Result is the full ranked population plus a convenience view of the
// rank-0 frontier sorted by crowding distance (descending — isolated
// boundary points first).
type Result struct {
	Frontier   []Solution
	All        []Solution // ordered: rank asc, then crowding desc, then EntityID asc
	Objectives []Objective
}

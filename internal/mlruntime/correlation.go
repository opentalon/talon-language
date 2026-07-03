package mlruntime

import (
	"context"
	"fmt"
	"math"
)

// FuncCorrelationPearson is the planner function name this primitive binds
// to. Must match planner.FuncCorrelationPearson — duplicated as a string to
// avoid an import cycle between the two packages.
const FuncCorrelationPearson = "correlation_pearson"

// MinCorrelationSample is the smallest number of (x, y) pairs the Pearson
// primitive accepts. Two points are always perfectly collinear (r = ±1), so
// a correlation is only meaningful from three points up.
const MinCorrelationSample = 3

// PearsonCorrelation computes the Pearson correlation coefficient r between
// two attributes across the matched record population, then gates the whole
// set on `r <op> threshold`. It is a population statistic — every entity in
// the input receives the same verdict (the coefficient describes the set,
// not the row) — so a `correlates_with` condition either keeps all
// candidates or none.
//
// Input rows are shaped [entity_id, …, value_x, …, value_y, …]; the column
// indices of the two series come from the Schema keys "value_x" / "value_y"
// (planner emits value_index_x / value_index_y). No training, no model — the
// coefficient is closed-form arithmetic and rides in the Explanation, so
// explainability stays Tier 1.
type PearsonCorrelation struct{}

// NewPearsonCorrelation constructs the primitive.
func NewPearsonCorrelation() *PearsonCorrelation { return &PearsonCorrelation{} }

// Name reports the planner function name this primitive serves.
func (pc *PearsonCorrelation) Name() string { return FuncCorrelationPearson }

// Compute reads the two parallel series, computes r, and returns one Result
// per entity carrying the shared verdict and the coefficient as evidence.
func (pc *PearsonCorrelation) Compute(_ context.Context, in Input) ([]Result, error) {
	idIdx := columnIndex(in.Schema, "entity_id", 0)
	xIdx := columnIndex(in.Schema, "value_x", 1)
	yIdx := columnIndex(in.Schema, "value_y", 2)

	op, _ := in.Params["op"].(string)
	if op == "" {
		op = ">"
	}
	threshold, _ := numericParam(in.Params, "threshold")

	// Collect the parallel (x, y) series — only rows where both are numeric.
	xs := make([]float64, 0, len(in.Rows))
	ys := make([]float64, 0, len(in.Rows))
	for _, row := range in.Rows {
		x, okX := numericAt(row, xIdx)
		y, okY := numericAt(row, yIdx)
		if okX && okY {
			xs = append(xs, x)
			ys = append(ys, y)
		}
	}
	if len(xs) < MinCorrelationSample {
		return nil, fmt.Errorf("%w: %d (x,y) pairs, need %d", ErrSampleTooSmall, len(xs), MinCorrelationSample)
	}

	r := pearson(xs, ys)
	flagged := compareOp(op, r, threshold)

	results := make([]Result, 0, len(in.Rows))
	for _, row := range in.Rows {
		if _, okX := numericAt(row, xIdx); !okX {
			continue
		}
		if _, okY := numericAt(row, yIdx); !okY {
			continue
		}
		entityID, _ := intAt(row, idIdx)
		results = append(results, Result{
			EntityID: entityID,
			Value:    flagged,
			Explanation: Explanation{
				Primitive: FuncCorrelationPearson,
				EntityID:  entityID,
				Inputs: map[string]any{
					"r":      r,
					"n":      len(xs),
					"attr_x": in.Params["attr_x"],
					"attr_y": in.Params["attr_y"],
				},
				Rules: []Rule{{
					Attr:     "correlation",
					Op:       op,
					Value:    threshold,
					Observed: r,
				}},
				Threshold: &Threshold{
					Method: "pearson",
					Value:  threshold,
					Sample: len(xs),
				},
			},
		})
	}
	return results, nil
}

// pearson returns the Pearson correlation coefficient of the parallel
// series xs and ys (equal length). It is 0 when either series has no
// variance (r undefined → treated as no correlation).
func pearson(xs, ys []float64) float64 {
	n := float64(len(xs))
	var sumX, sumY float64
	for i := range xs {
		sumX += xs[i]
		sumY += ys[i]
	}
	meanX, meanY := sumX/n, sumY/n

	var num, denX, denY float64
	for i := range xs {
		dx, dy := xs[i]-meanX, ys[i]-meanY
		num += dx * dy
		denX += dx * dx
		denY += dy * dy
	}
	den := math.Sqrt(denX * denY)
	if den == 0 {
		return 0
	}
	return num / den
}

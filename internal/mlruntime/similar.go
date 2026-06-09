package mlruntime

import (
	"context"
	"fmt"
	"math"
	"sort"
)

// CosineSimilarity satisfies the SimilarityCosine primitive. For each
// candidate entity, it computes the cosine similarity between the entity's
// feature vector and a target vector (either an attr value on a named
// "anchor" entity, or a literal vector passed via Params).
//
// Features are read from Input.Entities by attribute name. Missing
// attributes contribute zero to the vector. Numeric features participate
// directly; non-numeric features are skipped so the dot product stays
// well-defined.
//
// Params (set by the planner from the `find similar` block):
//
//	features  []string   — bare attribute names to use as the feature
//	                       vector. If absent, the primitive falls back to
//	                       a single attr provided via "attr" — matching
//	                       the one-axis form `to attr "id"`.
//	to_id     int        — the anchor entity whose feature vector is the
//	                       target. If absent, the primitive uses the
//	                       average feature vector of all candidates so
//	                       similarity scores rank rows by how typical
//	                       they are.
//	within    float64    — optional cosine-similarity threshold in
//	                       [-1, 1]. When set, only rows scoring ≥ within
//	                       are kept (Value=true); rows below are
//	                       Value=false with the raw score recorded in
//	                       the explanation.
//	top_k     int        — optional cap on the number of "kept" rows,
//	                       evaluated after the within filter. The
//	                       highest-scoring rows win ties.
//
// Result values: bool true/false per row (kept / dropped) so the
// downstream narrowing in narrowByML treats them like any other ML
// filter. The numeric cosine score is preserved in the explanation.
type CosineSimilarity struct{}

// NewCosineSimilarity constructs the primitive.
func NewCosineSimilarity() *CosineSimilarity { return &CosineSimilarity{} }

// Name returns the planner function constant this primitive serves.
func (*CosineSimilarity) Name() string { return "similarity_cosine" }

// Compute scores each candidate row by cosine similarity against the
// chosen target. See the type-level comment for the parameter contract.
func (s *CosineSimilarity) Compute(_ context.Context, in Input) ([]Result, error) {
	features := readStringSlice(in.Params, "features")
	if len(features) == 0 {
		if attr := readString(in.Params, "attr"); attr != "" {
			features = []string{attr}
		}
	}
	if len(features) == 0 {
		return nil, fmt.Errorf("similarity_cosine: at least one feature is required")
	}

	// Build a feature vector per candidate row. Rows[i][0] is the entity ID.
	type vec struct {
		id     int
		values []float64
	}
	vecs := make([]vec, 0, len(in.Rows))
	for _, row := range in.Rows {
		if len(row) == 0 {
			continue
		}
		id, ok := toInt(row[0])
		if !ok {
			continue
		}
		attrs := in.Entities[id]
		v := make([]float64, len(features))
		for i, name := range features {
			if val, ok := attrs[name]; ok {
				if f, ok := toFloat(val); ok {
					v[i] = f
				}
			}
		}
		vecs = append(vecs, vec{id: id, values: v})
	}

	// Decide on the target vector.
	var target []float64
	if anchorID, ok := readInt(in.Params, "to_id"); ok {
		for _, v := range vecs {
			if v.id == anchorID {
				target = v.values
				break
			}
		}
	}
	if target == nil {
		// Fall back to the centroid so the rankings still mean something.
		target = make([]float64, len(features))
		if len(vecs) > 0 {
			for _, v := range vecs {
				for i, f := range v.values {
					target[i] += f
				}
			}
			for i := range target {
				target[i] /= float64(len(vecs))
			}
		}
	}

	within, hasThreshold := readFloat(in.Params, "within")
	topK, _ := readInt(in.Params, "top_k")

	// Score every row.
	type scored struct {
		id    int
		score float64
	}
	scoredRows := make([]scored, 0, len(vecs))
	for _, v := range vecs {
		scoredRows = append(scoredRows, scored{id: v.id, score: cosine(v.values, target)})
	}

	// Sort descending by score so the top_k pick is straightforward and
	// the output ordering is deterministic.
	sort.SliceStable(scoredRows, func(i, j int) bool {
		return scoredRows[i].score > scoredRows[j].score
	})

	// Apply the within threshold, then top_k.
	kept := 0
	results := make([]Result, 0, len(scoredRows))
	for _, sr := range scoredRows {
		passWithin := !hasThreshold || sr.score >= within
		passTopK := topK == 0 || kept < topK
		keep := passWithin && passTopK
		if keep {
			kept++
		}
		results = append(results, Result{
			EntityID: sr.id,
			Value:    keep,
			Explanation: Explanation{
				Primitive:  "similarity_cosine",
				EntityID:   sr.id,
				Confidence: sr.score,
				Inputs:     map[string]any{"score": sr.score, "features": features},
			},
		})
	}
	return results, nil
}

// cosine returns the cosine similarity between two equal-length vectors.
// Returns 0 when either vector has zero magnitude — the math is
// undefined and 0 is the convention everyone in numerical code agrees on.
func cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

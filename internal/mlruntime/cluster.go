package mlruntime

import (
	"context"
	"fmt"
	"math"
)

// clusterPoint is one row's feature vector + its entity ID. Hoisted to
// file scope so the helpers (neighborsOf, defaultEps) can name the
// slice element type without going through any.
type clusterPoint struct {
	id     int
	vector []float64
}

// DBSCANCluster satisfies the ClusterDBSCAN primitive. It runs DBSCAN
// over feature vectors built from each candidate entity's attributes,
// producing one (entity → cluster ID) assignment per row.
//
// DBSCAN is density-based — it discovers clusters of arbitrary shape
// without needing the caller to specify k up front, which matches the
// Talon `cluster by [attr "X", attr "Y"]` block's intent. Noise points
// (rows that don't fit any cluster) are tagged with cluster ID -1 and
// returned with Value=false; clustered rows return their cluster ID as
// Value (float64).
//
// Params:
//
//	features  []string  — bare attribute names to use as the feature
//	                      vector. Each candidate entity contributes one
//	                      vector of len(features) floats. Non-numeric
//	                      attributes contribute zero (categorical
//	                      one-hot encoding is a follow-up).
//	eps       float64   — neighbourhood radius. Default: 0.5 of the
//	                      feature vector's range, computed from the
//	                      data so callers don't have to guess.
//	min_pts   int       — minimum neighbours (including the point itself)
//	                      required to form a core point. Default: 3,
//	                      matching the small-data regime Talon targets.
type DBSCANCluster struct{}

// NewDBSCANCluster constructs the primitive.
func NewDBSCANCluster() *DBSCANCluster { return &DBSCANCluster{} }

// Name returns the planner function constant this primitive serves.
func (*DBSCANCluster) Name() string { return "cluster_dbscan" }

// Compute clusters the input rows. See the type-level comment for the
// parameter contract.
func (d *DBSCANCluster) Compute(_ context.Context, in Input) ([]Result, error) {
	features := readStringSlice(in.Params, "features")
	if len(features) == 0 {
		features = readStringSlice(in.Params, "by")
	}
	if len(features) == 0 {
		return nil, fmt.Errorf("cluster_dbscan: at least one feature is required")
	}

	points := make([]clusterPoint, 0, len(in.Rows))
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
		points = append(points, clusterPoint{id: id, vector: v})
	}

	eps, hasEps := readFloat(in.Params, "eps")
	if !hasEps || eps <= 0 {
		eps = defaultEps(points)
	}
	minPts, hasMinPts := readInt(in.Params, "min_pts")
	if !hasMinPts || minPts <= 0 {
		minPts = 3
	}

	// DBSCAN proper. cluster[i] = -1 means unvisited / noise;
	// 0+ are valid cluster IDs.
	labels := make([]int, len(points))
	for i := range labels {
		labels[i] = -2 // unvisited
	}
	clusterID := 0
	for i := range points {
		if labels[i] != -2 {
			continue
		}
		neighbors := neighborsOf(points, i, eps)
		if len(neighbors) < minPts {
			labels[i] = -1 // noise
			continue
		}
		labels[i] = clusterID
		// Expand the cluster: walk every density-reachable neighbour.
		queue := append([]int(nil), neighbors...)
		for len(queue) > 0 {
			j := queue[0]
			queue = queue[1:]
			if labels[j] == -1 {
				// previously-labelled noise becomes a border point of
				// this cluster
				labels[j] = clusterID
			}
			if labels[j] != -2 {
				continue
			}
			labels[j] = clusterID
			ns := neighborsOf(points, j, eps)
			if len(ns) >= minPts {
				queue = append(queue, ns...)
			}
		}
		clusterID++
	}

	results := make([]Result, 0, len(points))
	for i, p := range points {
		label := labels[i]
		if label == -2 {
			label = -1 // any still-unvisited row is noise (shouldn't happen)
		}
		results = append(results, Result{
			EntityID: p.id,
			Value:    float64(label),
			Explanation: Explanation{
				Primitive: "cluster_dbscan",
				EntityID:  p.id,
				Inputs: map[string]any{
					"cluster_id": label,
					"eps":        eps,
					"min_pts":    minPts,
					"features":   features,
				},
			},
		})
	}
	return results, nil
}

// neighborsOf returns the indices of all points within `eps` of points[i]
// (including i itself, matching DBSCAN's convention).
func neighborsOf(points []clusterPoint, i int, eps float64) []int {
	var out []int
	for j := range points {
		if euclidean(points[i].vector, points[j].vector) <= eps {
			out = append(out, j)
		}
	}
	return out
}

// euclidean returns the L2 distance between two equal-length vectors.
func euclidean(a, b []float64) float64 {
	if len(a) != len(b) {
		return math.Inf(1)
	}
	var sum float64
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return math.Sqrt(sum)
}

// defaultEps picks a sensible eps when the caller didn't supply one.
// Heuristic: take the average pairwise distance and use 50% of it as the
// neighbourhood radius. Small datasets at Talon's scale don't justify
// the full k-distance-plot calibration DBSCAN normally needs.
func defaultEps(points []clusterPoint) float64 {
	if len(points) < 2 {
		return 1.0
	}
	var sum float64
	var count int
	for i := 0; i < len(points); i++ {
		for j := i + 1; j < len(points); j++ {
			sum += euclidean(points[i].vector, points[j].vector)
			count++
		}
	}
	if count == 0 {
		return 1.0
	}
	avg := sum / float64(count)
	if avg == 0 {
		return 1.0
	}
	return avg * 0.5
}

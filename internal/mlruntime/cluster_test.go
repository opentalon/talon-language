package mlruntime

import (
	"context"
	"testing"
)

// Two tight clusters on the X axis. DBSCAN with a reasonable eps should
// separate them into two distinct cluster IDs and tag no rows as noise.
func TestDBSCANTwoSeparatedClusters(t *testing.T) {
	prim := NewDBSCANCluster()
	res, err := prim.Compute(context.Background(), Input{
		Rows: [][]any{{1.0}, {2.0}, {3.0}, {4.0}, {5.0}, {6.0}},
		Entities: map[int]map[string]any{
			1: {"x": 0.0}, 2: {"x": 0.1}, 3: {"x": 0.2},
			4: {"x": 10.0}, 5: {"x": 10.1}, 6: {"x": 10.2},
		},
		Params: map[string]any{
			"features": []string{"x"},
			"eps":      0.5,
			"min_pts":  2,
		},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	// Map ID → cluster
	clusters := map[int]float64{}
	for _, r := range res {
		clusters[r.EntityID] = r.Value.(float64)
	}
	// 1,2,3 should share a cluster ID; 4,5,6 should share another.
	if clusters[1] != clusters[2] || clusters[2] != clusters[3] {
		t.Errorf("low-x rows should cluster together; got %v", clusters)
	}
	if clusters[4] != clusters[5] || clusters[5] != clusters[6] {
		t.Errorf("high-x rows should cluster together; got %v", clusters)
	}
	if clusters[1] == clusters[4] {
		t.Errorf("the two groups should be in different clusters; got both at %v", clusters[1])
	}
	for _, c := range clusters {
		if c < 0 {
			t.Errorf("no row should be noise with this fixture; got %v", clusters)
		}
	}
}

// Isolated outlier in a sea of clustered points: the outlier should be
// labelled noise (-1) while the cluster gets a non-negative ID.
func TestDBSCANIsolatesNoise(t *testing.T) {
	prim := NewDBSCANCluster()
	res, _ := prim.Compute(context.Background(), Input{
		Rows: [][]any{{1.0}, {2.0}, {3.0}, {99.0}},
		Entities: map[int]map[string]any{
			1: {"x": 0.0, "y": 0.0},
			2: {"x": 0.1, "y": 0.1},
			3: {"x": 0.05, "y": 0.0},
			99: {"x": 50.0, "y": 50.0},
		},
		Params: map[string]any{
			"features": []string{"x", "y"},
			"eps":      0.5,
			"min_pts":  2,
		},
	})
	clusters := map[int]float64{}
	for _, r := range res {
		clusters[r.EntityID] = r.Value.(float64)
	}
	if clusters[99] != -1 {
		t.Errorf("outlier entity 99 should be labelled noise (-1); got %v", clusters[99])
	}
	if clusters[1] == -1 || clusters[2] == -1 || clusters[3] == -1 {
		t.Errorf("clustered rows should not be noise; got %v", clusters)
	}
}

func TestDBSCANRequiresFeatures(t *testing.T) {
	prim := NewDBSCANCluster()
	_, err := prim.Compute(context.Background(), Input{
		Rows:   [][]any{{1.0}},
		Params: map[string]any{},
	})
	if err == nil {
		t.Error("expected an error when no features are configured")
	}
}

func TestDBSCANAcceptsByAlias(t *testing.T) {
	// The planner emits `by` (for `cluster "X" { by attr "..." }`). The
	// primitive accepts both spellings to match the language surface.
	prim := NewDBSCANCluster()
	res, err := prim.Compute(context.Background(), Input{
		Rows: [][]any{{1.0}, {2.0}},
		Entities: map[int]map[string]any{
			1: {"x": 0.0}, 2: {"x": 0.1},
		},
		Params: map[string]any{
			"by":      []string{"x"},
			"eps":     0.5,
			"min_pts": 2,
		},
	})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(res) != 2 {
		t.Errorf("want 2 results, got %d", len(res))
	}
}

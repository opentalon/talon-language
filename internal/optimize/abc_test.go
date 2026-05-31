package optimize

import (
	"math"
	"testing"
)

// TestABC_FindsKnownMaximum is the headline correctness test: maximize a
// 1-D smooth function with a known maximum at x=2 inside the search box.
// f(x) = -(x - 2)² + 10 peaks at (2, 10).
func TestABC_FindsKnownMaximum(t *testing.T) {
	fitness := func(x []float64) float64 {
		return -(x[0]-2)*(x[0]-2) + 10
	}
	res := ABC(fitness, []ABCBounds{{Min: -5, Max: 5}}, ABCConfig{Seed: 42})

	if math.Abs(res.Best[0]-2.0) > 0.1 {
		t.Errorf("optimum x: want ~2.0, got %v", res.Best[0])
	}
	if math.Abs(res.Fitness-10.0) > 0.5 {
		t.Errorf("optimum fitness: want ~10, got %v", res.Fitness)
	}
}

func TestABC_TwoDimSphere(t *testing.T) {
	// Minimize x² + y² → maximize -(x² + y²); optimum (0,0) → fitness 0.
	fitness := func(x []float64) float64 {
		return -(x[0]*x[0] + x[1]*x[1])
	}
	res := ABC(fitness, []ABCBounds{{Min: -10, Max: 10}, {Min: -10, Max: 10}},
		ABCConfig{Seed: 7, Iterations: 100})

	if math.Abs(res.Best[0]) > 0.5 || math.Abs(res.Best[1]) > 0.5 {
		t.Errorf("optimum: want near (0,0), got (%v, %v)", res.Best[0], res.Best[1])
	}
}

func TestABC_Deterministic(t *testing.T) {
	fit := func(x []float64) float64 { return -math.Abs(x[0] - 1.5) }
	r1 := ABC(fit, []ABCBounds{{Min: -10, Max: 10}}, ABCConfig{Seed: 99})
	r2 := ABC(fit, []ABCBounds{{Min: -10, Max: 10}}, ABCConfig{Seed: 99})
	if math.Abs(r1.Best[0]-r2.Best[0]) > 1e-12 {
		t.Errorf("seed 99 not deterministic: %v vs %v", r1.Best[0], r2.Best[0])
	}
}

func TestABC_HistoryNonDecreasing(t *testing.T) {
	fitness := func(x []float64) float64 { return -math.Abs(x[0]-3) - math.Abs(x[1]+2) }
	res := ABC(fitness, []ABCBounds{{Min: -10, Max: 10}, {Min: -10, Max: 10}},
		ABCConfig{Seed: 13})
	for i := 1; i < len(res.History); i++ {
		if res.History[i] < res.History[i-1]-1e-12 {
			t.Errorf("history[%d]=%v < history[%d]=%v — best-so-far must be non-decreasing",
				i, res.History[i], i-1, res.History[i-1])
		}
	}
}

func TestABC_EmptyBoundsDoesNotPanic(t *testing.T) {
	res := ABC(func([]float64) float64 { return 0 }, nil, ABCConfig{})
	if len(res.Best) != 0 || res.Fitness != 0 {
		t.Errorf("empty bounds: want zero result, got %+v", res)
	}
}

func TestBinaryF1_PerfectClassifier(t *testing.T) {
	pred := map[int]bool{1: true, 2: true, 3: true}
	act := map[int]bool{1: true, 2: true, 3: true}
	if f := BinaryF1(pred, act); f != 1.0 {
		t.Errorf("perfect classifier: want F1=1.0, got %v", f)
	}
}

func TestBinaryF1_HalfPrecisionHalfRecall(t *testing.T) {
	// 2 TP, 2 FP → precision = 0.5
	// 2 TP, 2 FN → recall = 0.5
	// F1 = 2 * 0.5 * 0.5 / (0.5 + 0.5) = 0.5
	pred := map[int]bool{1: true, 2: true, 3: true, 4: true}
	act := map[int]bool{1: true, 2: true, 5: true, 6: true}
	if f := BinaryF1(pred, act); math.Abs(f-0.5) > 1e-9 {
		t.Errorf("F1: want 0.5, got %v", f)
	}
}

func TestBinaryF1_NoTruePositives(t *testing.T) {
	pred := map[int]bool{1: true, 2: true}
	act := map[int]bool{3: true, 4: true}
	if f := BinaryF1(pred, act); f != 0 {
		t.Errorf("no overlap: want F1=0, got %v", f)
	}
}

func TestBinaryF1_OnePredictedTwoActual(t *testing.T) {
	// pred={1}, act={1,2}: TP=1, FP=0, FN=1 → P=1, R=0.5, F1=2/3
	pred := map[int]bool{1: true}
	act := map[int]bool{1: true, 2: true}
	p, r := BinaryPrecisionRecall(pred, act)
	if p != 1.0 {
		t.Errorf("precision: want 1.0, got %v", p)
	}
	if math.Abs(r-0.5) > 1e-9 {
		t.Errorf("recall: want 0.5, got %v", r)
	}
}

// TestABC_TunesThresholdAgainstLabels demonstrates the actual production use:
// given a labeled dataset where the "real" anomaly threshold is z=2.0 (any
// value with |z|>2 is positive), ABC must rediscover ~2.0 by maximizing F1.
func TestABC_TunesThresholdAgainstLabels(t *testing.T) {
	// 20 samples with mean=50, stddev≈10. Anything beyond |z|=2 (i.e. ±20 from
	// mean) is labeled positive.
	values := []float64{
		48, 51, 49, 50, 52, 49, 50, 51, 50, 49, // normal cluster
		47, 53, 50, 49, 51, // more normal
		90, 95, // big positives (z ≈ +4, +4.5)
		10, 5, // big negatives (z ≈ -4, -4.5)
		70, // mild outlier (z ≈ +2.0)
	}
	mean, stddev := meanStdDev(values)

	actual := map[int]bool{}
	for i, v := range values {
		if math.Abs((v-mean)/stddev) > 2.0 {
			actual[i] = true
		}
	}

	fitness := func(x []float64) float64 {
		threshold := x[0]
		pred := map[int]bool{}
		for i, v := range values {
			if math.Abs((v-mean)/stddev) > threshold {
				pred[i] = true
			}
		}
		return BinaryF1(pred, actual)
	}

	res := ABC(fitness, []ABCBounds{{Min: 0.5, Max: 4.0}}, ABCConfig{Seed: 42, Iterations: 50})
	if res.Fitness < 0.9 {
		t.Errorf("expected F1 > 0.9 on labeled data, got %v at threshold %v", res.Fitness, res.Best[0])
	}
}

func meanStdDev(xs []float64) (float64, float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	m := sum / float64(len(xs))
	var sq float64
	for _, x := range xs {
		d := x - m
		sq += d * d
	}
	return m, math.Sqrt(sq / float64(len(xs)))
}

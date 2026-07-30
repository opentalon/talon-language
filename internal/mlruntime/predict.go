package mlruntime

import (
	"context"
	"fmt"
	"sort"
	"strconv"
)

// DecisionTreePredictor implements the predict_decision_tree primitive: it
// trains a CART classification tree (Gini-impurity splits) on the labeled
// training rows, then walks each candidate down the tree to a leaf. The
// predicted class is the leaf's majority label; confidence is the leaf's
// purity (fraction of training rows at that leaf sharing the label).
//
// Trees are chosen over opaque models because the decision path is the
// explanation: "at risk because operating_hours > 2000 AND repair_count > 3"
// reads straight out of the splits taken (RFC #6, ADR-0007). The path rides
// in the Explanation (both as human-readable Rules and a raw string list).
//
// Features are read by bare attribute name from Input.Entities (candidates)
// and Input.Training (labeled examples). No normalisation is needed: axis-
// aligned threshold splits are scale-invariant per feature.
//
// Params (set by the planner from the predict block):
//
//	feature_names    []string — bare attribute names the splits range over.
//	label_attr       string   — informational; the runtime has already read
//	                            it into each TrainingRow.Label.
//	max_depth        int      — tree depth cap (default 5).
//	min_samples_leaf int      — a split must leave at least this many rows on
//	                            each side (default 5); guards against
//	                            overfitting to noise.
//	confidence       float64  — optional; the runtime drops predictions whose
//	                            leaf purity is below this. The primitive still
//	                            emits every prediction with its purity.
//
// Result.Value is the predicted class string.
type DecisionTreePredictor struct{}

// NewDecisionTreePredictor constructs the primitive.
func NewDecisionTreePredictor() *DecisionTreePredictor { return &DecisionTreePredictor{} }

// Name returns the planner function constant this primitive serves.
func (*DecisionTreePredictor) Name() string { return "predict_decision_tree" }

// treeNode is one node of the trained CART tree: a leaf carries a class +
// purity; an internal node carries an axis-aligned split (feature ≤ threshold
// goes left).
type treeNode struct {
	leaf        bool
	class       string
	purity      float64
	featureIdx  int
	featureName string
	threshold   float64
	left, right *treeNode
}

// trainSample is one labeled training row projected onto the feature axes.
type trainSample struct {
	vec   []float64
	label string
}

// Compute trains the tree and classifies every candidate. See the type-level
// comment for the parameter contract.
func (d *DecisionTreePredictor) Compute(_ context.Context, in Input) ([]Result, error) {
	features := readStringSlice(in.Params, "feature_names")
	if len(features) == 0 {
		return nil, fmt.Errorf("predict_decision_tree: at least one feature is required")
	}
	// A `using model "..."` predict block ships a pre-fitted tree inline — walk
	// it directly, no training. This is the tree's true fitted representation
	// (the splits + leaves), the eager-model counterpart to kNN's inline
	// examples.
	if nodes, ok := in.Params["fitted_tree"].([]FittedTreeNode); ok && len(nodes) > 0 {
		tree := buildFittedTree(nodes, features)
		return classifyWithTree(tree, features, in), nil
	}
	if len(in.Training) == 0 {
		// No labeled examples reached the primitive (e.g. the executor path;
		// see ADR-0006/0007). Emit no predictions rather than erroring, so a
		// `talon run` degrades gracefully instead of aborting.
		return nil, nil
	}
	maxDepth := readIntOr(in.Params, "max_depth", 5)
	minLeaf := readIntOr(in.Params, "min_samples_leaf", 5)
	if minLeaf < 1 {
		minLeaf = 1
	}

	samples := make([]trainSample, len(in.Training))
	for i, t := range in.Training {
		samples[i] = trainSample{vec: featureVec(t.Attrs, features), label: t.Label}
	}
	tree := buildTree(samples, features, 0, maxDepth, minLeaf)
	return classifyWithTree(tree, features, in), nil
}

// classifyWithTree walks each candidate down the tree and emits a prediction
// with its decision path. Shared by the trained and pre-fitted paths.
func classifyWithTree(tree *treeNode, features []string, in Input) []Result {
	results := make([]Result, 0, len(in.Rows))
	for _, row := range in.Rows {
		if len(row) == 0 {
			continue
		}
		id, ok := toInt(row[0])
		if !ok {
			continue
		}
		vec := featureVec(in.Entities[id], features)
		class, purity, rules, path := tree.walk(vec)
		results = append(results, Result{
			EntityID: id,
			Value:    class,
			Explanation: Explanation{
				Primitive:  "predict_decision_tree",
				EntityID:   id,
				Confidence: purity,
				Rules:      rules,
				Inputs: map[string]any{
					"class":         class,
					"decision_path": path,
				},
			},
		})
	}
	return results
}

// FittedTreeNode is one node of a serialised decision tree carried inline by a
// `model` block's `fitted tree { ... }`. Nodes are flat and index-referenced:
// an internal node names a feature, a threshold, and the Left/Right child
// indices (feature ≤ threshold goes Left); a leaf carries a class + purity.
type FittedTreeNode struct {
	Index     int
	Leaf      bool
	Class     string
	Purity    float64
	Feature   string
	Threshold float64
	Left      int
	Right     int
}

// buildFittedTree reconstructs the internal treeNode graph from a flat node
// list, resolving feature names to axis indices via features. The root is the
// node with index 0.
func buildFittedTree(nodes []FittedTreeNode, features []string) *treeNode {
	byIdx := make(map[int]FittedTreeNode, len(nodes))
	for _, n := range nodes {
		byIdx[n.Index] = n
	}
	fidx := make(map[string]int, len(features))
	for i, f := range features {
		fidx[f] = i
	}
	var build func(i int, depth int) *treeNode
	build = func(i, depth int) *treeNode {
		n, ok := byIdx[i]
		if !ok || depth > len(nodes) { // missing ref / cycle guard
			return &treeNode{leaf: true}
		}
		if n.Leaf {
			return &treeNode{leaf: true, class: n.Class, purity: n.Purity}
		}
		return &treeNode{
			featureIdx:  fidx[n.Feature],
			featureName: n.Feature,
			threshold:   n.Threshold,
			left:        build(n.Left, depth+1),
			right:       build(n.Right, depth+1),
		}
	}
	return build(0, 0)
}

// buildTree recursively grows a CART tree. It stops (returns a leaf) when the
// samples are pure, the depth cap is hit, or no split leaves min_samples_leaf
// rows on both sides with a positive Gini decrease.
func buildTree(samples []trainSample, features []string, depth, maxDepth, minLeaf int) *treeNode {
	class, purity := majorityClass(samples)
	if purity == 1.0 || depth >= maxDepth || len(samples) < 2*minLeaf {
		return &treeNode{leaf: true, class: class, purity: purity}
	}
	split := bestSplit(samples, features, minLeaf)
	if split == nil {
		return &treeNode{leaf: true, class: class, purity: purity}
	}
	left, right := partition(samples, split.featureIdx, split.threshold)
	return &treeNode{
		featureIdx:  split.featureIdx,
		featureName: features[split.featureIdx],
		threshold:   split.threshold,
		left:        buildTree(left, features, depth+1, maxDepth, minLeaf),
		right:       buildTree(right, features, depth+1, maxDepth, minLeaf),
	}
}

type candidateSplit struct {
	featureIdx int
	threshold  float64
	decrease   float64
}

// bestSplit scans every feature and candidate threshold (midpoints between
// consecutive distinct values) for the split that most reduces Gini impurity,
// subject to min_samples_leaf on both sides. Returns nil if none qualifies.
func bestSplit(samples []trainSample, features []string, minLeaf int) *candidateSplit {
	parent := gini(samples)
	var best *candidateSplit
	for f := range features {
		vals := make([]float64, len(samples))
		for i, s := range samples {
			vals[i] = s.vec[f]
		}
		for _, thr := range thresholds(vals) {
			left, right := partition(samples, f, thr)
			if len(left) < minLeaf || len(right) < minLeaf {
				continue
			}
			n := float64(len(samples))
			weighted := float64(len(left))/n*gini(left) + float64(len(right))/n*gini(right)
			// Gini decrease is always ≥ 0 (splitting never raises weighted
			// impurity). Take the best valid split even when the decrease is
			// zero: XOR-style problems have no useful *first* split, but a
			// second split under it resolves them. Depth / min_samples_leaf /
			// purity are what stop the recursion.
			dec := parent - weighted
			if best == nil || dec > best.decrease {
				best = &candidateSplit{featureIdx: f, threshold: thr, decrease: dec}
			}
		}
	}
	return best
}

// thresholds returns candidate split points: the midpoints between
// consecutive distinct sorted values.
func thresholds(vals []float64) []float64 {
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	var out []float64
	for i := 1; i < len(sorted); i++ {
		if sorted[i] != sorted[i-1] {
			out = append(out, (sorted[i]+sorted[i-1])/2)
		}
	}
	return out
}

// partition splits samples on feature f: value ≤ threshold goes left.
func partition(samples []trainSample, f int, threshold float64) (left, right []trainSample) {
	for _, s := range samples {
		if s.vec[f] <= threshold {
			left = append(left, s)
		} else {
			right = append(right, s)
		}
	}
	return left, right
}

// gini returns the Gini impurity of a sample set's label distribution:
// 1 − Σ pᵢ². 0 means perfectly pure.
func gini(samples []trainSample) float64 {
	if len(samples) == 0 {
		return 0
	}
	counts := map[string]int{}
	for _, s := range samples {
		counts[s.label]++
	}
	n := float64(len(samples))
	imp := 1.0
	for _, c := range counts {
		p := float64(c) / n
		imp -= p * p
	}
	return imp
}

// majorityClass returns the most common label and its purity (fraction of the
// set). Ties break to the lexically smallest label for determinism.
func majorityClass(samples []trainSample) (string, float64) {
	counts := map[string]int{}
	for _, s := range samples {
		counts[s.label]++
	}
	label, n := majorityVote(counts) // shared with classify_knn; lexical tiebreak
	if len(samples) == 0 {
		return label, 0
	}
	return label, float64(n) / float64(len(samples))
}

// walk routes a candidate feature vector root→leaf, returning the predicted
// class, the leaf purity, the structured split Rules, and a human-readable
// decision path.
func (n *treeNode) walk(vec []float64) (string, float64, []Rule, []string) {
	var rules []Rule
	var path []string
	node := n
	for !node.leaf {
		observed := 0.0
		if node.featureIdx < len(vec) {
			observed = vec[node.featureIdx]
		}
		op := ">"
		next := node.right
		if observed <= node.threshold {
			op = "<="
			next = node.left
		}
		rules = append(rules, Rule{
			Attr:     node.featureName,
			Op:       op,
			Value:    node.threshold,
			Observed: observed,
		})
		path = append(path, node.featureName+" "+op+" "+strconv.FormatFloat(node.threshold, 'g', -1, 64))
		node = next
	}
	return node.class, node.purity, rules, path
}

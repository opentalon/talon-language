package mlruntime

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strconv"
	"sync"

	"github.com/opentalon/talon-language/internal/factstore"
)

// FuncPPRTopK is the planner function name this primitive binds to.
// Must match planner.FuncPPRTopK.
const FuncPPRTopK = "ppr_topk"

// Defaults for the PPR primitive. Tunable via Input.Params.
const (
	DefaultDamping    = 0.85
	DefaultTolerance  = 1e-6
	DefaultMaxIter    = 100
	DefaultPPRTopK    = 10
	MinPPRSeeds       = 1
	MaxPPRCacheSlots  = 4 // cache up to this many distinct snapshots per primitive
)

var (
	ErrEmptySeeds       = errors.New("ppr: at least one seed is required")
	ErrNoGraph          = errors.New("ppr: no graph snapshot supplied")
	ErrSeedNotInGraph   = errors.New("ppr: seed not present in graph")
	ErrInvalidDamping   = errors.New("ppr: damping must be in [0, 1)")
	ErrInvalidTolerance = errors.New("ppr: tolerance must be > 0")
)

// PersonalizedPageRank ranks entities by random-walk affinity to a given
// seed set, computed via power iteration over a GraphSnapshot.
//
// Determinism guarantees:
//   - Output is sorted by score descending, then by node ID ascending.
//   - Iteration uses arrays only — never maps — on the hot path.
//   - Same snapshot + seeds + params always yield byte-identical Results.
//
// See docs/design/0002-ppr-fact-graph.md.
type PersonalizedPageRank struct {
	mu    sync.RWMutex
	cache map[string]*cachedSnapshot
}

type cachedSnapshot struct {
	snap *factstore.GraphSnapshot
}

// NewPersonalizedPageRank returns a fresh primitive instance with an
// empty snapshot cache.
func NewPersonalizedPageRank() *PersonalizedPageRank {
	return &PersonalizedPageRank{cache: map[string]*cachedSnapshot{}}
}

// Name implements Primitive.
func (p *PersonalizedPageRank) Name() string { return FuncPPRTopK }

// Compute runs PPR on the supplied graph and returns at most top_k Results
// sorted by score. The graph snapshot is passed in via Input.Params["graph"]
// (a *factstore.GraphSnapshot). Seeds may be passed as Params["seeds"]
// (slice of entity IDs as strings) or Params["seed"] (single entity ID).
func (p *PersonalizedPageRank) Compute(_ context.Context, in Input) ([]Result, error) {
	graph, _ := in.Params["graph"].(*factstore.GraphSnapshot)
	if graph == nil {
		return nil, ErrNoGraph
	}

	seeds, err := collectSeeds(in.Params)
	if err != nil {
		return nil, err
	}
	if len(seeds) < MinPPRSeeds {
		return nil, ErrEmptySeeds
	}

	damping, err := dampingParam(in.Params)
	if err != nil {
		return nil, err
	}
	tolerance, err := toleranceParam(in.Params)
	if err != nil {
		return nil, err
	}
	maxIter := intParamOrDefault(in.Params, "max_iterations", DefaultMaxIter)
	if maxIter <= 0 {
		maxIter = DefaultMaxIter
	}
	topK := intParamOrDefault(in.Params, "top_k", DefaultPPRTopK)
	if topK <= 0 {
		topK = DefaultPPRTopK
	}
	includeSeeds := boolParam(in.Params, "include_seeds")

	if graph.NodeCount() == 0 {
		return nil, nil
	}

	personalization := make([]float64, graph.NodeCount())
	seedIdx := make(map[int]struct{}, len(seeds))
	missing := 0
	for _, s := range seeds {
		idx, ok := graph.NodeIndex[s]
		if !ok {
			missing++
			continue
		}
		personalization[idx] = 1
		seedIdx[idx] = struct{}{}
	}
	if len(seedIdx) == 0 {
		return nil, fmt.Errorf("%w: none of %v found", ErrSeedNotInGraph, seeds)
	}
	// Normalize personalization to sum to 1.
	for i, v := range personalization {
		if v > 0 {
			personalization[i] = v / float64(len(seedIdx))
		}
	}

	// Row-normalize edge weights (random walk transition matrix).
	rowSums := make([]float64, graph.NodeCount())
	for i, ws := range graph.EdgeWeights {
		var s float64
		for _, w := range ws {
			s += w
		}
		rowSums[i] = s
	}

	rank := make([]float64, graph.NodeCount())
	copy(rank, personalization)
	next := make([]float64, graph.NodeCount())

	converged := false
	var iterations int
	for iterations = 0; iterations < maxIter; iterations++ {
		// next = (1-damping) * personalization + damping * P^T * rank
		for i := range next {
			next[i] = (1 - damping) * personalization[i]
		}
		// Distribute mass from each node along outgoing edges.
		// Dangling nodes (rowSums[i] == 0) leak their mass back uniformly
		// onto the personalization vector — standard random-surfer fix.
		var dangling float64
		for i, sum := range rowSums {
			if sum == 0 {
				dangling += rank[i]
				continue
			}
			contribution := damping * rank[i] / sum
			for j, nbr := range graph.EdgesFrom[i] {
				next[nbr] += contribution * graph.EdgeWeights[i][j]
			}
		}
		if dangling > 0 {
			share := damping * dangling
			for i := range next {
				next[i] += share * personalization[i]
			}
		}

		// L1 distance check.
		var delta float64
		for i := range next {
			delta += math.Abs(next[i] - rank[i])
		}
		rank, next = next, rank
		if delta < tolerance {
			iterations++
			converged = true
			break
		}
	}

	type scored struct {
		idx int
		val float64
	}
	scoredAll := make([]scored, 0, graph.NodeCount())
	for i, s := range rank {
		if !includeSeeds {
			if _, isSeed := seedIdx[i]; isSeed {
				continue
			}
		}
		scoredAll = append(scoredAll, scored{idx: i, val: s})
	}
	// Sort: score desc, then node ID asc (deterministic).
	sort.SliceStable(scoredAll, func(a, b int) bool {
		if scoredAll[a].val != scoredAll[b].val {
			return scoredAll[a].val > scoredAll[b].val
		}
		return graph.Nodes[scoredAll[a].idx] < graph.Nodes[scoredAll[b].idx]
	})
	if topK < len(scoredAll) {
		scoredAll = scoredAll[:topK]
	}

	results := make([]Result, 0, len(scoredAll))
	for rankIdx, s := range scoredAll {
		entityStr := graph.Nodes[s.idx]
		entityID, _ := strconv.Atoi(entityStr)
		// Fallback: hash non-numeric IDs into a stable 32-bit int so the
		// downstream extractFlaggedIDs can keep a numeric set.
		if entityStr != strconv.Itoa(entityID) {
			h := fnv.New32a()
			_, _ = h.Write([]byte(entityStr))
			entityID = int(h.Sum32())
		}
		results = append(results, Result{
			EntityID: entityID,
			Value:    s.val,
			Explanation: Explanation{
				Primitive: FuncPPRTopK,
				EntityID:  entityID,
				Inputs: map[string]any{
					"entity":        entityStr,
					"seeds":         seeds,
					"damping":       damping,
					"tolerance":     tolerance,
					"iterations":    iterations,
					"converged":     converged,
					"graph_nodes":   graph.NodeCount(),
					"graph_edges":   graph.EdgeCount(),
					"graph_version": graph.Version,
					"score":         s.val,
					"rank":          rankIdx + 1,
				},
				Rules: []Rule{{
					Attr:     "ppr_score",
					Op:       "topk",
					Value:    topK,
					Observed: s.val,
				}},
			},
		})
	}
	return results, nil
}

// SnapshotForCache stores a snapshot under the given key, evicting the
// oldest entry when capacity is reached. Used by the executor to memoise
// graph builds across plan invocations.
func (p *PersonalizedPageRank) SnapshotForCache(key string, snap *factstore.GraphSnapshot) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cache == nil {
		p.cache = map[string]*cachedSnapshot{}
	}
	if len(p.cache) >= MaxPPRCacheSlots {
		// Evict an arbitrary slot — small cache, no LRU bookkeeping needed.
		for k := range p.cache {
			delete(p.cache, k)
			break
		}
	}
	p.cache[key] = &cachedSnapshot{snap: snap}
}

// CachedSnapshot retrieves a snapshot previously stored via SnapshotForCache,
// returning (snap, true) if the version still matches or (nil, false) if the
// caller should rebuild.
func (p *PersonalizedPageRank) CachedSnapshot(key string, version int64) (*factstore.GraphSnapshot, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	c, ok := p.cache[key]
	if !ok || c.snap == nil {
		return nil, false
	}
	if c.snap.Version != version {
		return nil, false
	}
	return c.snap, true
}

func collectSeeds(params map[string]any) ([]string, error) {
	out := []string{}
	switch v := params["seeds"].(type) {
	case []string:
		out = append(out, v...)
	case []any:
		for _, item := range v {
			out = append(out, anyToEntityID(item))
		}
	}
	if s, ok := params["seed"]; ok {
		out = append(out, anyToEntityID(s))
	}
	// Drop empties.
	cleaned := out[:0]
	for _, s := range out {
		if s != "" {
			cleaned = append(cleaned, s)
		}
	}
	return cleaned, nil
}

func anyToEntityID(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.Itoa(int(x))
	}
	return ""
}

func dampingParam(params map[string]any) (float64, error) {
	v, ok := numericParam(params, "damping")
	if !ok {
		return DefaultDamping, nil
	}
	if v < 0 || v >= 1 {
		return 0, fmt.Errorf("%w: got %v", ErrInvalidDamping, v)
	}
	return v, nil
}

func toleranceParam(params map[string]any) (float64, error) {
	v, ok := numericParam(params, "tolerance")
	if !ok {
		return DefaultTolerance, nil
	}
	if v <= 0 {
		return 0, fmt.Errorf("%w: got %v", ErrInvalidTolerance, v)
	}
	return v, nil
}

func intParamOrDefault(params map[string]any, key string, def int) int {
	if v, ok := numericParam(params, key); ok {
		return int(v)
	}
	return def
}

func boolParam(params map[string]any, key string) bool {
	if params == nil {
		return false
	}
	v, _ := params[key].(bool)
	return v
}

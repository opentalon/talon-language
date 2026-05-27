package factstore

import (
	"errors"
	"sort"
	"strconv"
)

// GraphSnapshot is an immutable, entity-to-entity projection of the fact
// store used by the find-related (PPR) primitive. Adjacency is stored in
// CSR form: EdgesFrom[i] is the sorted list of node indices reachable from
// node i, and EdgeWeights[i] is the parallel weight slice.
//
// Snapshots are values. Once returned, callers MUST treat them as
// read-only. The Version field is a monotonic counter exposed by the
// underlying store so callers can detect when to rebuild a cached graph.
type GraphSnapshot struct {
	Nodes       []string          // sorted entity IDs
	NodeIndex   map[string]int    // entity ID → position in Nodes
	EdgesFrom   [][]int           // CSR adjacency, sorted ascending per row
	EdgeWeights [][]float64       // parallel weights for EdgesFrom
	Version     int64             // store-provided generation counter
	Attributes  []string          // sorted attribute names included in this snapshot
}

// SnapshotOptions controls graph construction.
//
// AttributeWeights reserves the API surface for per-attribute edge weighting.
// In v1 all included attributes weight 1.0; if AttributeWeights is non-empty,
// only listed attributes contribute and each contributes the given weight.
//
// MaxBucketSize caps the number of entities that may share a single
// (attribute, value) bucket before that bucket is skipped to avoid hub
// explosion. A bucket of size N produces N*(N-1)/2 edges. Default 500.
type SnapshotOptions struct {
	EntityTypes      []string
	IncludeAttrs     []string
	ExcludeAttrs     []string
	AttributeWeights map[string]float64
	MaxBucketSize    int
}

// DefaultMaxBucketSize is the per-bucket entity cap used when
// SnapshotOptions.MaxBucketSize is 0.
const DefaultMaxBucketSize = 500

// ErrSnapshotUnsupported is returned by stores that cannot build graph
// snapshots. The PPR primitive surfaces this as a diagnostic and skips
// rather than failing the whole plan.
var ErrSnapshotUnsupported = errors.New("factstore: graph snapshot unsupported")

// FactTriple is the minimal entity-attribute-value tuple that
// BuildSnapshotFromTriples consumes. Stores that want to feed their data
// into the PPR primitive convert their facts into this slice.
type FactTriple struct {
	Entity    string
	Attribute string
	Value     any
}

// BuildSnapshotFromTriples constructs a GraphSnapshot from a slice of
// (entity, attribute, value) triples. Two entities share an edge when they
// share a (attribute, value) pair. If a value is itself an entity ID in
// the entity set, a direct edge from owner to target is also emitted.
//
// The version counter is opaque — pass a monotonically increasing value
// from the caller (e.g. fact-store generation, or a hash of the dataset).
func BuildSnapshotFromTriples(triples []FactTriple, version int64, opts SnapshotOptions) *GraphSnapshot {
	maxBucket := opts.MaxBucketSize
	if maxBucket <= 0 {
		maxBucket = DefaultMaxBucketSize
	}

	includeAttr := buildAttrFilter(opts.IncludeAttrs, opts.ExcludeAttrs)
	weights := opts.AttributeWeights

	nodeSet := map[string]struct{}{}
	for _, t := range triples {
		nodeSet[t.Entity] = struct{}{}
	}
	nodes := make([]string, 0, len(nodeSet))
	for n := range nodeSet {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	idx := make(map[string]int, len(nodes))
	for i, n := range nodes {
		idx[n] = i
	}

	type bucketKey struct {
		Attr string
		Val  string
	}
	buckets := map[bucketKey][]int{}
	attrSet := map[string]struct{}{}

	// Edge weight accumulator: undirected adjacency keyed by sorted (i,j).
	type edgeKey struct {
		Lo, Hi int
	}
	edges := map[edgeKey]float64{}

	addEdge := func(a, b int, w float64) {
		if a == b {
			return
		}
		lo, hi := a, b
		if lo > hi {
			lo, hi = hi, lo
		}
		edges[edgeKey{lo, hi}] += w
	}

	for _, t := range triples {
		if !includeAttr(t.Attribute) {
			continue
		}
		w := 1.0
		if weights != nil {
			if v, ok := weights[t.Attribute]; ok {
				w = v
			} else if len(weights) > 0 {
				continue
			}
		}
		attrSet[t.Attribute] = struct{}{}

		ent, ok := idx[t.Entity]
		if !ok {
			continue
		}

		key := bucketKey{Attr: t.Attribute, Val: valueKey(t.Value)}
		buckets[key] = append(buckets[key], ent)

		// Direct entity-valued edges
		if sv, ok := t.Value.(string); ok {
			if other, ok := idx[sv]; ok {
				addEdge(ent, other, w)
			}
		}
	}

	for key, members := range buckets {
		if len(members) > maxBucket {
			continue
		}
		w := 1.0
		if weights != nil {
			if v, ok := weights[key.Attr]; ok {
				w = v
			}
		}
		// Dedup within the bucket so an entity that owns the same
		// (attr,value) twice does not weight itself.
		sort.Ints(members)
		uniq := members[:0]
		for i, m := range members {
			if i == 0 || m != members[i-1] {
				uniq = append(uniq, m)
			}
		}
		for i := 0; i < len(uniq); i++ {
			for j := i + 1; j < len(uniq); j++ {
				addEdge(uniq[i], uniq[j], w)
			}
		}
	}

	from := make([][]int, len(nodes))
	wts := make([][]float64, len(nodes))
	type neighbor struct {
		idx int
		w   float64
	}
	tmp := make([][]neighbor, len(nodes))
	for k, w := range edges {
		tmp[k.Lo] = append(tmp[k.Lo], neighbor{idx: k.Hi, w: w})
		tmp[k.Hi] = append(tmp[k.Hi], neighbor{idx: k.Lo, w: w})
	}
	for i := range tmp {
		sort.Slice(tmp[i], func(a, b int) bool { return tmp[i][a].idx < tmp[i][b].idx })
		from[i] = make([]int, len(tmp[i]))
		wts[i] = make([]float64, len(tmp[i]))
		for j, n := range tmp[i] {
			from[i][j] = n.idx
			wts[i][j] = n.w
		}
	}

	attrs := make([]string, 0, len(attrSet))
	for a := range attrSet {
		attrs = append(attrs, a)
	}
	sort.Strings(attrs)

	return &GraphSnapshot{
		Nodes:       nodes,
		NodeIndex:   idx,
		EdgesFrom:   from,
		EdgeWeights: wts,
		Version:     version,
		Attributes:  attrs,
	}
}

// NodeCount returns the number of nodes in the snapshot.
func (g *GraphSnapshot) NodeCount() int { return len(g.Nodes) }

// EdgeCount returns the number of distinct undirected edges (each edge
// appears once in this count even though EdgesFrom stores both endpoints).
func (g *GraphSnapshot) EdgeCount() int {
	n := 0
	for _, row := range g.EdgesFrom {
		n += len(row)
	}
	return n / 2
}

func buildAttrFilter(include, exclude []string) func(string) bool {
	if len(include) == 0 && len(exclude) == 0 {
		return func(string) bool { return true }
	}
	inc := map[string]struct{}{}
	for _, a := range include {
		inc[a] = struct{}{}
	}
	exc := map[string]struct{}{}
	for _, a := range exclude {
		exc[a] = struct{}{}
	}
	return func(a string) bool {
		if _, ok := exc[a]; ok {
			return false
		}
		if len(inc) == 0 {
			return true
		}
		_, ok := inc[a]
		return ok
	}
}

func valueKey(v any) string {
	switch x := v.(type) {
	case string:
		return "s:" + x
	case bool:
		if x {
			return "b:1"
		}
		return "b:0"
	case int:
		return "i:" + itoa(x)
	case int64:
		return "i:" + itoa(int(x))
	case float64:
		// Stable canonical form: integer-valued floats fold into the int key
		// so 3 and 3.0 share a bucket.
		if x == float64(int64(x)) {
			return "i:" + itoa(int(x))
		}
		return "f:" + ftoa(x)
	case nil:
		return "z:"
	}
	return "?:"
}

func itoa(n int) string { return strconv.Itoa(n) }

func ftoa(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

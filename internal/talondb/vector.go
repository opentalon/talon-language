package talondb

import (
	"context"

	pb "github.com/opentalon/talon-db/proto/talondbpb"
)

// VectorMetric mirrors talondbpb.VectorMetric for callers that don't
// want to import the proto types directly. Cosine is the default; pass
// Unspecified to let the server pick (server-side fall-through is also
// Cosine).
type VectorMetric int

const (
	// VectorMetricUnspecified asks the server to use its default
	// metric (Cosine today). Honoured only on the first insert into a
	// (entity, scope) pair; later inserts keep the scope's original
	// metric regardless.
	VectorMetricUnspecified VectorMetric = iota
	// VectorMetricCosine is 1 - (a·b / (|a| |b|)).
	VectorMetricCosine
	// VectorMetricEuclidean is sqrt(Σ (a_i - b_i)²).
	VectorMetricEuclidean
)

func (m VectorMetric) toProto() pb.VectorMetric {
	switch m {
	case VectorMetricCosine:
		return pb.VectorMetric_VECTOR_METRIC_COSINE
	case VectorMetricEuclidean:
		return pb.VectorMetric_VECTOR_METRIC_EUCLIDEAN
	default:
		return pb.VectorMetric_VECTOR_METRIC_UNSPECIFIED
	}
}

func vectorMetricFromProto(m pb.VectorMetric) VectorMetric {
	switch m {
	case pb.VectorMetric_VECTOR_METRIC_EUCLIDEAN:
		return VectorMetricEuclidean
	case pb.VectorMetric_VECTOR_METRIC_COSINE:
		return VectorMetricCosine
	default:
		return VectorMetricUnspecified
	}
}

// VectorHit is one nearest-neighbour result from VectorSearch. Distance
// semantics follow the scope's metric (lower = closer for both Cosine
// and Euclidean).
type VectorHit struct {
	ID       string
	Distance float32
}

// VectorScope describes one of the tenant's vector scopes — embedding
// model name, the dimension that got locked on first insert, the
// number of live (non-tombstoned) vectors, and the configured metric.
type VectorScope struct {
	Scope  string
	Dim    int
	Count  int
	Metric VectorMetric
}

// VectorInsert writes a single vector under the client's tenant.
// Dimension is locked on the first insert into a (scope) — callers
// must size every subsequent vector identically or receive an
// ErrInvalidArgument from the server. The metric is honoured only on
// the first call; later inserts keep the scope's original metric.
func (c *Client) VectorInsert(ctx context.Context, scope, id string, vec []float32, metric VectorMetric) error {
	if _, err := c.svc.VectorInsert(ctx, &pb.VectorInsertRequest{
		EntityId: c.Tenant(),
		Scope:    scope,
		Id:       id,
		Vector:   vec,
		Metric:   metric.toProto(),
	}); err != nil {
		return mapStatusError(err)
	}
	return nil
}

// VectorSearch returns the k nearest neighbours of query under
// (tenant, scope), ordered closest first. The server clamps k to the
// scope's current cardinality and skips tombstoned ids, so callers
// don't need to pad or post-filter.
func (c *Client) VectorSearch(ctx context.Context, scope string, query []float32, k int) ([]VectorHit, error) {
	resp, err := c.svc.VectorSearch(ctx, &pb.VectorSearchRequest{
		EntityId: c.Tenant(),
		Scope:    scope,
		Vector:   query,
		K:        int32(k),
	})
	if err != nil {
		return nil, mapStatusError(err)
	}
	hits := resp.GetHits()
	out := make([]VectorHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, VectorHit{ID: h.GetId(), Distance: h.GetDistance()})
	}
	return out, nil
}

// VectorDelete tombstones the vector at (tenant, scope, id). Returns
// errors.Is(err, ErrNotFound) when the id never existed in the scope.
func (c *Client) VectorDelete(ctx context.Context, scope, id string) error {
	if _, err := c.svc.VectorDelete(ctx, &pb.VectorDeleteRequest{
		EntityId: c.Tenant(),
		Scope:    scope,
		Id:       id,
	}); err != nil {
		return mapStatusError(err)
	}
	return nil
}

// VectorDropScope removes every vector under (tenant, scope) and
// clears the dimension lock so a later VectorInsert may use a
// different dimension. Returns ErrNotFound when the scope never
// existed — calling DropScope twice is not idempotent on purpose so
// callers notice misconfiguration.
func (c *Client) VectorDropScope(ctx context.Context, scope string) error {
	if _, err := c.svc.VectorDropScope(ctx, &pb.VectorDropScopeRequest{
		EntityId: c.Tenant(),
		Scope:    scope,
	}); err != nil {
		return mapStatusError(err)
	}
	return nil
}

// VectorListScopes returns every scope under the client's tenant,
// sorted by name. Returns an empty slice (not an error) when the
// tenant has never written a vector.
func (c *Client) VectorListScopes(ctx context.Context) ([]VectorScope, error) {
	resp, err := c.svc.VectorListScopes(ctx, &pb.VectorListScopesRequest{
		EntityId: c.Tenant(),
	})
	if err != nil {
		return nil, mapStatusError(err)
	}
	scopes := resp.GetScopes()
	out := make([]VectorScope, 0, len(scopes))
	for _, s := range scopes {
		out = append(out, VectorScope{
			Scope:  s.GetScope(),
			Dim:    int(s.GetDim()),
			Count:  int(s.GetCount()),
			Metric: vectorMetricFromProto(s.GetMetric()),
		})
	}
	return out, nil
}


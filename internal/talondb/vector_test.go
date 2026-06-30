package talondb_test

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"

	talondbadapter "github.com/opentalon/talon-language/internal/talondb"

	"github.com/opentalon/talon-db/bboltstore"
	"github.com/opentalon/talon-db/grpcserver"
	pb "github.com/opentalon/talon-db/proto/talondbpb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// dialVectorAdapter wires the same real-server / real-bbolt path the
// pull and pushdown tests use, so vector wire tests exercise the
// production code paths end-to-end.
func dialVectorAdapter(t *testing.T) (*talondbadapter.Client, func()) {
	t.Helper()
	store, err := bboltstore.Open(filepath.Join(t.TempDir(), "vec.bbolt"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	pb.RegisterTalonDBServiceServer(srv, grpcserver.New(store, store.Events(), "test"))
	go func() { _ = srv.Serve(lis) }()
	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) { return lis.Dial() }))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c := talondbadapter.NewClientFromService(pb.NewTalonDBServiceClient(conn)).WithTenant("tenant-a")
	return c, func() {
		_ = conn.Close()
		srv.GracefulStop()
		_ = store.Close()
	}
}

func TestClientVectorInsertSearch(t *testing.T) {
	t.Parallel()
	c, cleanup := dialVectorAdapter(t)
	defer cleanup()
	ctx := context.Background()

	for i, v := range [][]float32{
		{1, 0, 0},
		{0, 1, 0},
		{0, 0, 1},
	} {
		if err := c.VectorInsert(ctx, "embed3", string(rune('a'+i)), v, talondbadapter.VectorMetricCosine); err != nil {
			t.Fatalf("Insert %d: %v", i, err)
		}
	}
	hits, err := c.VectorSearch(ctx, "embed3", []float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 || hits[0].ID != "a" {
		t.Fatalf("hits = %v", hits)
	}
}

func TestClientVectorDeleteReturnsErrNotFound(t *testing.T) {
	// Typed-error contract: the adapter surfaces talondb.ErrNotFound
	// for missing ids, so callers can use errors.Is instead of
	// scraping the message.
	t.Parallel()
	c, cleanup := dialVectorAdapter(t)
	defer cleanup()
	ctx := context.Background()

	_ = c.VectorInsert(ctx, "s", "a", []float32{1, 0, 0}, talondbadapter.VectorMetricCosine)

	err := c.VectorDelete(ctx, "s", "missing")
	if !errors.Is(err, talondbadapter.ErrNotFound) {
		t.Errorf("delete missing: want ErrNotFound, got %v", err)
	}
	if err := c.VectorDelete(ctx, "s", "a"); err != nil {
		t.Fatalf("delete existing: %v", err)
	}
	hits, _ := c.VectorSearch(ctx, "s", []float32{1, 0, 0}, 5)
	for _, h := range hits {
		if h.ID == "a" {
			t.Fatalf("a should be tombstoned, hits = %v", hits)
		}
	}
}

func TestClientVectorDropScopeAndListScopes(t *testing.T) {
	t.Parallel()
	c, cleanup := dialVectorAdapter(t)
	defer cleanup()
	ctx := context.Background()

	_ = c.VectorInsert(ctx, "v3", "a", []float32{1, 0, 0}, talondbadapter.VectorMetricCosine)
	_ = c.VectorInsert(ctx, "v3", "b", []float32{0, 1, 0}, talondbadapter.VectorMetricCosine)
	_ = c.VectorInsert(ctx, "v4", "x", []float32{1, 0, 0, 0}, talondbadapter.VectorMetricEuclidean)

	scopes, err := c.VectorListScopes(ctx)
	if err != nil {
		t.Fatalf("ListScopes: %v", err)
	}
	if len(scopes) != 2 {
		t.Fatalf("scopes = %v", scopes)
	}
	if scopes[0].Scope != "v3" || scopes[0].Dim != 3 || scopes[0].Count != 2 {
		t.Errorf("scopes[0] = %+v", scopes[0])
	}
	if scopes[1].Scope != "v4" || scopes[1].Metric != talondbadapter.VectorMetricEuclidean {
		t.Errorf("scopes[1] = %+v", scopes[1])
	}

	if err := c.VectorDropScope(ctx, "v3"); err != nil {
		t.Fatalf("DropScope: %v", err)
	}
	// Second DropScope on the same name is a NotFound — callers
	// should treat this as misconfiguration, not idempotency.
	if err := c.VectorDropScope(ctx, "v3"); !errors.Is(err, talondbadapter.ErrNotFound) {
		t.Errorf("drop twice: want ErrNotFound, got %v", err)
	}

	// After drop the dim lock is gone — a 5-dim insert into the same
	// scope name succeeds.
	if err := c.VectorInsert(ctx, "v3", "new", []float32{1, 0, 0, 0, 0}, talondbadapter.VectorMetricCosine); err != nil {
		t.Fatalf("post-drop insert: %v", err)
	}
}

func TestClientVectorDimensionMismatchIsInvalidArgument(t *testing.T) {
	t.Parallel()
	c, cleanup := dialVectorAdapter(t)
	defer cleanup()
	ctx := context.Background()

	_ = c.VectorInsert(ctx, "s", "a", []float32{1, 0, 0}, talondbadapter.VectorMetricCosine)
	err := c.VectorInsert(ctx, "s", "b", []float32{1, 0}, talondbadapter.VectorMetricCosine)
	if !errors.Is(err, talondbadapter.ErrInvalidArgument) {
		t.Errorf("dim mismatch: want ErrInvalidArgument, got %v", err)
	}
}

func TestClientVectorTenantIsolation(t *testing.T) {
	// Two clients on the same connection differ only by tenant. A
	// search under tenant-b must never see tenant-a's vectors.
	t.Parallel()
	ca, cleanup := dialVectorAdapter(t)
	defer cleanup()
	cb := ca.WithTenant("tenant-b")
	ctx := context.Background()

	if err := ca.VectorInsert(ctx, "s", "a1", []float32{1, 0, 0}, talondbadapter.VectorMetricCosine); err != nil {
		t.Fatalf("a insert: %v", err)
	}
	if err := cb.VectorInsert(ctx, "s", "b1", []float32{1, 0, 0, 0}, talondbadapter.VectorMetricCosine); err != nil {
		t.Fatalf("b insert: %v", err)
	}
	res, err := cb.VectorSearch(ctx, "s", []float32{1, 0, 0, 0}, 5)
	if err != nil {
		t.Fatalf("b search: %v", err)
	}
	if len(res) != 1 || res[0].ID != "b1" {
		t.Errorf("tenant b leak: %v", res)
	}
}

func TestClientVectorEmptyTenantHasNoScopes(t *testing.T) {
	t.Parallel()
	c, cleanup := dialVectorAdapter(t)
	defer cleanup()
	scopes, err := c.VectorListScopes(context.Background())
	if err != nil {
		t.Fatalf("ListScopes: %v", err)
	}
	if len(scopes) != 0 {
		t.Errorf("fresh tenant should have no scopes: %v", scopes)
	}
}

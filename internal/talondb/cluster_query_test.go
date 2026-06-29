package talondb_test

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	adapterpkg "github.com/opentalon/talon-language/internal/talondb"

	"github.com/opentalon/talon-db/bboltstore"
	"github.com/opentalon/talon-db/grpcserver"
	pb "github.com/opentalon/talon-db/proto/talondbpb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// dialAdapterWithRealStore wires:
//
//	real bbolt-backed Store → grpcserver behind a bufconn listener →
//	pb.TalonDBServiceClient → adapter.Client wrapper.
//
// No mocks. Anything that breaks at any layer surfaces here.
func dialAdapterWithRealStore(t *testing.T) (*adapterpkg.Client, pb.TalonDBServiceClient, func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cluster.bbolt")
	store, err := bboltstore.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	pb.RegisterTalonDBServiceServer(srv, grpcserver.New(store, store.Events(), "test"))
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return lis.Dial()
		}))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	rawSvc := pb.NewTalonDBServiceClient(conn)
	adapterClient := adapterpkg.NewClientFromService(rawSvc).WithTenant("tenant-a")

	return adapterClient, rawSvc, func() {
		_ = conn.Close()
		srv.GracefulStop()
		_ = store.Close()
	}
}

// putEvent writes a temporal-shaped doc directly via the gRPC client so
// the test exercises the real talon-db storage path (bbolt + temporal
// index + emit) end-to-end.
func putEvent(t *testing.T, raw pb.TalonDBServiceClient, docID, itemID, recordType string, at time.Time) {
	t.Helper()
	doc := fmt.Sprintf(`{"item_id":%q,"type":%q,"at":%d}`, itemID, recordType, at.UnixNano())
	if _, err := raw.Put(context.Background(), &pb.PutRequest{
		EntityId: "tenant-a", DocId: docID, Doc: []byte(doc),
	}); err != nil {
		t.Fatalf("Put %s: %v", docID, err)
	}
}

func TestClusterQueryDetectsThreeWithinNinetyDays(t *testing.T) {
	t.Parallel()
	c, raw, cleanup := dialAdapterWithRealStore(t)
	defer cleanup()
	ctx := context.Background()

	day := 24 * time.Hour
	base := time.Unix(0, 0)
	// Three failures within 30 days, then a long quiet gap, then two
	// more (not enough alone).
	putEvent(t, raw, "e1", "truck-7", "failure", base)
	putEvent(t, raw, "e2", "truck-7", "failure", base.Add(5*day))
	putEvent(t, raw, "e3", "truck-7", "failure", base.Add(25*day))
	putEvent(t, raw, "e4", "truck-7", "failure", base.Add(200*day))
	putEvent(t, raw, "e5", "truck-7", "failure", base.Add(205*day))

	clusters, err := c.ClusterQuery(ctx, "truck-7", []string{"failure"}, 90*day, 3)
	if err != nil {
		t.Fatalf("ClusterQuery: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("got %d clusters, want 1: %+v", len(clusters), clusters)
	}
	c0 := clusters[0]
	if len(c0.Events) != 3 {
		t.Fatalf("cluster size %d, want 3", len(c0.Events))
	}
	if c0.First.UnixNano() != base.UnixNano() {
		t.Errorf("First = %d, want %d", c0.First.UnixNano(), base.UnixNano())
	}
	if c0.Last.UnixNano() != base.Add(25*day).UnixNano() {
		t.Errorf("Last = %d, want %d", c0.Last.UnixNano(), base.Add(25*day).UnixNano())
	}
	// Event docIDs flow back through the wire correctly.
	want := map[string]bool{"e1": true, "e2": true, "e3": true}
	for _, ev := range c0.Events {
		if !want[ev.DocID] {
			t.Errorf("unexpected docID in cluster: %s", ev.DocID)
		}
	}
}

func TestClusterQueryFiltersByType(t *testing.T) {
	t.Parallel()
	c, raw, cleanup := dialAdapterWithRealStore(t)
	defer cleanup()
	ctx := context.Background()

	day := 24 * time.Hour
	base := time.Unix(0, 0)
	putEvent(t, raw, "e1", "truck-7", "failure", base)
	putEvent(t, raw, "e2", "truck-7", "inspection", base.Add(1*day))
	putEvent(t, raw, "e3", "truck-7", "failure", base.Add(2*day))
	putEvent(t, raw, "e4", "truck-7", "inspection", base.Add(3*day))
	putEvent(t, raw, "e5", "truck-7", "failure", base.Add(4*day))

	clusters, err := c.ClusterQuery(ctx, "truck-7", []string{"failure"}, 90*day, 3)
	if err != nil {
		t.Fatalf("ClusterQuery: %v", err)
	}
	if len(clusters) != 1 || len(clusters[0].Events) != 3 {
		t.Fatalf("expected 1 cluster of 3 failures, got %+v", clusters)
	}
	for _, ev := range clusters[0].Events {
		if ev.Type != "failure" {
			t.Errorf("non-failure event leaked: %v", ev)
		}
	}
}

func TestClusterQueryEmptyForAbsentItem(t *testing.T) {
	t.Parallel()
	c, _, cleanup := dialAdapterWithRealStore(t)
	defer cleanup()

	clusters, err := c.ClusterQuery(context.Background(), "nobody", nil, 24*time.Hour, 3)
	if err != nil {
		t.Fatalf("ClusterQuery: %v", err)
	}
	if len(clusters) != 0 {
		t.Fatalf("expected empty result, got %+v", clusters)
	}
}

func TestClusterQueryNarrowWindowNoClusters(t *testing.T) {
	t.Parallel()
	c, raw, cleanup := dialAdapterWithRealStore(t)
	defer cleanup()
	ctx := context.Background()

	day := 24 * time.Hour
	base := time.Unix(0, 0)
	putEvent(t, raw, "e1", "truck-7", "failure", base)
	putEvent(t, raw, "e2", "truck-7", "failure", base.Add(10*day))
	putEvent(t, raw, "e3", "truck-7", "failure", base.Add(20*day))

	clusters, err := c.ClusterQuery(ctx, "truck-7", nil, 5*day, 3)
	if err != nil {
		t.Fatalf("ClusterQuery: %v", err)
	}
	if len(clusters) != 0 {
		t.Fatalf("expected no clusters with 5-day window over 20-day span, got %+v", clusters)
	}
}

func TestClusterQueryTenantIsolation(t *testing.T) {
	t.Parallel()
	c, raw, cleanup := dialAdapterWithRealStore(t)
	defer cleanup()
	ctx := context.Background()

	day := 24 * time.Hour
	base := time.Unix(0, 0)
	// Three events for truck-7 in tenant-b — should be invisible to
	// our tenant-a client.
	for i, at := range []time.Time{base, base.Add(day), base.Add(2 * day)} {
		doc := fmt.Sprintf(`{"item_id":"truck-7","type":"failure","at":%d}`, at.UnixNano())
		if _, err := raw.Put(ctx, &pb.PutRequest{
			EntityId: "tenant-b", DocId: fmt.Sprintf("e%d", i), Doc: []byte(doc),
		}); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	clusters, err := c.ClusterQuery(ctx, "truck-7", nil, 90*day, 3)
	if err != nil {
		t.Fatalf("ClusterQuery: %v", err)
	}
	if len(clusters) != 0 {
		t.Fatalf("tenant isolation broken: %+v", clusters)
	}
}

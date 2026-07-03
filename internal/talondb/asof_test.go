package talondb_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/opentalon/talon-language/internal/factstore"
	adapterpkg "github.com/opentalon/talon-language/internal/talondb"

	"github.com/opentalon/talon-db/bboltstore"
	"github.com/opentalon/talon-db/grpcserver"
	pb "github.com/opentalon/talon-db/proto/talondbpb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// dialAsOf wires real bbolt → grpcserver → Adapter and exposes the store
// so the test can drive its clock, exercising QueryAsOf (the TimeTraveler
// capability) end-to-end over the wire.
func dialAsOf(t *testing.T) (*bboltstore.Store, *adapterpkg.Adapter, func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "asof.bbolt")
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
	raw := pb.NewTalonDBServiceClient(conn)
	a := adapterpkg.New(adapterpkg.NewClientFromService(raw).WithTenant("tenant-a"))
	return store, a, func() {
		_ = conn.Close()
		srv.GracefulStop()
		_ = store.Close()
	}
}

// TestServerQueryAsOfDivergence proves talon-db time-travel end-to-end:
// a machine certified in the past but defective now matches an as-of
// certified query only at the earlier instant; one never certified never
// matches.
func TestServerQueryAsOfDivergence(t *testing.T) {
	store, a, cleanup := dialAsOf(t)
	defer cleanup()
	ctx := context.Background()

	t0 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	t1 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	store.SetClock(func() time.Time { return t0 })
	if err := a.Assert(ctx, []factstore.Fact{
		{RecordID: "501", Attribute: ":record/type", Value: "machine"},
		{RecordID: "501", Attribute: ":record/status", Value: "certified"},
		{RecordID: "503", Attribute: ":record/type", Value: "machine"},
		{RecordID: "503", Attribute: ":record/status", Value: "defective"},
	}); err != nil {
		t.Fatalf("assert v1: %v", err)
	}

	store.SetClock(func() time.Time { return t1 })
	if err := a.Assert(ctx, []factstore.Fact{
		{RecordID: "501", Attribute: ":record/status", Value: "defective"},
	}); err != nil {
		t.Fatalf("assert v2: %v", err)
	}

	statusIDs := func(status string, asOf time.Time) map[int]bool {
		rows, err := a.QueryAsOf(ctx, factstore.Query{
			Find: []string{"?e"},
			Where: []factstore.Clause{
				&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/status", Value: factstore.Lit(status)},
			},
		}, asOf)
		if err != nil {
			t.Fatalf("QueryAsOf %q: %v", status, err)
		}
		out := map[int]bool{}
		for _, r := range rows {
			if f, ok := r[0].(float64); ok {
				out[int(f)] = true
			}
		}
		return out
	}

	mid := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	after := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	if got := statusIDs("certified", mid); !got[501] || got[503] {
		t.Errorf("certified as-of mid = %v, want {501}", got)
	}
	if got := statusIDs("certified", after); got[501] {
		t.Errorf("certified as-of after: 501 should have regressed, got %v", got)
	}
	if got := statusIDs("defective", after); !got[501] || !got[503] {
		t.Errorf("defective as-of after = %v, want {501,503}", got)
	}
}

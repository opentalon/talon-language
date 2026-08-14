package talondb_test

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	adapterpkg "github.com/opentalon/tln-language/internal/talondb"

	"github.com/opentalon/talon-db/bboltstore"
	"github.com/opentalon/talon-db/grpcserver"
	pb "github.com/opentalon/talon-db/proto/talondbpb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// dialSequenceAdapter wires a real bboltstore behind grpcserver behind
// bufconn so SequenceJoin runs against the actual storage path.
func dialSequenceAdapter(t *testing.T) (*adapterpkg.Client, pb.TalonDBServiceClient, func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "seq.bbolt")
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
	c := adapterpkg.NewClientFromService(raw).WithTenant("tenant-a")
	return c, raw, func() {
		_ = conn.Close()
		srv.GracefulStop()
		_ = store.Close()
	}
}

func putSeqEvent(t *testing.T, raw pb.TalonDBServiceClient, docID, itemID, recordType string, at time.Time) {
	t.Helper()
	doc := fmt.Sprintf(`{"item_id":%q,"type":%q,"at":%d}`, itemID, recordType, at.UnixNano())
	if _, err := raw.Put(context.Background(), &pb.PutRequest{
		EntityId: "tenant-a", DocId: docID, Doc: []byte(doc),
	}); err != nil {
		t.Fatalf("Put %s: %v", docID, err)
	}
}

func TestSequenceJoinFindsInspectFollowedByFault(t *testing.T) {
	t.Parallel()
	c, raw, cleanup := dialSequenceAdapter(t)
	defer cleanup()
	ctx := context.Background()

	day := 24 * time.Hour
	base := time.Unix(0, 0)
	// truck-1: inspect → fault within 10 days → match
	putSeqEvent(t, raw, "e1", "truck-1", "inspect", base)
	putSeqEvent(t, raw, "e2", "truck-1", "fault", base.Add(10*day))
	// truck-2: fault → inspect → no match for inspect-then-fault
	putSeqEvent(t, raw, "e3", "truck-2", "fault", base)
	putSeqEvent(t, raw, "e4", "truck-2", "inspect", base.Add(5*day))

	matches, err := c.SequenceJoin(ctx, nil, []string{"inspect", "fault"}, 30*day)
	if err != nil {
		t.Fatalf("SequenceJoin: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("got %d matches, want 1 (truck-1): %+v", len(matches), matches)
	}
	if matches[0].ItemID != "truck-1" {
		t.Fatalf("matched item = %q, want truck-1", matches[0].ItemID)
	}
	if len(matches[0].Events) != 2 {
		t.Fatalf("match events = %d, want 2", len(matches[0].Events))
	}
	if matches[0].Events[0].Type != "inspect" || matches[0].Events[1].Type != "fault" {
		t.Fatalf("events out of order: %+v", matches[0].Events)
	}
}

func TestSequenceJoinWindowExcludesFarPair(t *testing.T) {
	t.Parallel()
	c, raw, cleanup := dialSequenceAdapter(t)
	defer cleanup()
	ctx := context.Background()

	day := 24 * time.Hour
	base := time.Unix(0, 0)
	putSeqEvent(t, raw, "e1", "truck-1", "inspect", base)
	putSeqEvent(t, raw, "e2", "truck-1", "fault", base.Add(200*day))

	matches, err := c.SequenceJoin(ctx, nil, []string{"inspect", "fault"}, 30*day)
	if err != nil {
		t.Fatalf("SequenceJoin: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no matches outside window, got %+v", matches)
	}
}

func TestSequenceJoinItemIDFilter(t *testing.T) {
	t.Parallel()
	c, raw, cleanup := dialSequenceAdapter(t)
	defer cleanup()
	ctx := context.Background()

	day := 24 * time.Hour
	base := time.Unix(0, 0)
	putSeqEvent(t, raw, "e1", "truck-1", "inspect", base)
	putSeqEvent(t, raw, "e2", "truck-1", "fault", base.Add(1*day))
	putSeqEvent(t, raw, "e3", "truck-2", "inspect", base)
	putSeqEvent(t, raw, "e4", "truck-2", "fault", base.Add(1*day))

	matches, err := c.SequenceJoin(ctx, []string{"truck-2"}, []string{"inspect", "fault"}, 0)
	if err != nil {
		t.Fatalf("SequenceJoin: %v", err)
	}
	if len(matches) != 1 || matches[0].ItemID != "truck-2" {
		t.Fatalf("expected truck-2 only, got %+v", matches)
	}
}

func TestSequenceJoinLongerStepChain(t *testing.T) {
	t.Parallel()
	c, raw, cleanup := dialSequenceAdapter(t)
	defer cleanup()
	ctx := context.Background()

	base := time.Unix(0, 0)
	putSeqEvent(t, raw, "e1", "case-7", "open", base)
	putSeqEvent(t, raw, "e2", "case-7", "investigate", base.Add(time.Hour))
	putSeqEvent(t, raw, "e3", "case-7", "resolve", base.Add(2*time.Hour))

	matches, err := c.SequenceJoin(ctx, nil, []string{"open", "investigate", "resolve"}, 0)
	if err != nil {
		t.Fatalf("SequenceJoin: %v", err)
	}
	if len(matches) != 1 || len(matches[0].Events) != 3 {
		t.Fatalf("expected single 3-event match, got %+v", matches)
	}
}

func TestSequenceJoinTenantIsolation(t *testing.T) {
	t.Parallel()
	c, raw, cleanup := dialSequenceAdapter(t)
	defer cleanup()
	ctx := context.Background()

	// Write the sequence under tenant-b — our tenant-a client should see nothing.
	base := time.Unix(0, 0)
	for i, ev := range []struct {
		typ string
		at  time.Time
	}{{"inspect", base}, {"fault", base.Add(time.Hour)}} {
		doc := fmt.Sprintf(`{"item_id":"truck-1","type":%q,"at":%d}`, ev.typ, ev.at.UnixNano())
		if _, err := raw.Put(ctx, &pb.PutRequest{
			EntityId: "tenant-b", DocId: fmt.Sprintf("e%d", i), Doc: []byte(doc),
		}); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	matches, err := c.SequenceJoin(ctx, nil, []string{"inspect", "fault"}, 0)
	if err != nil {
		t.Fatalf("SequenceJoin: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("tenant isolation broken: %+v", matches)
	}
}

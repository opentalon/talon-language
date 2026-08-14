package talondb_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"github.com/opentalon/tln-language/internal/factstore"
	adapterpkg "github.com/opentalon/tln-language/internal/talondb"

	"github.com/opentalon/talon-db/bboltstore"
	"github.com/opentalon/talon-db/grpcserver"
	pb "github.com/opentalon/talon-db/proto/talondbpb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// dialServerQuery wires real bbolt → real grpcserver → real adapter
// Client so Client.Query exercises the server-side composer end-to-end.
func dialServerQuery(t *testing.T) (*adapterpkg.Client, func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "q.bbolt")
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
	return c, func() {
		_ = conn.Close()
		srv.GracefulStop()
		_ = store.Close()
	}
}

// seedFleetThroughAdapter writes the same fleet-shaped fixture every
// test uses by funnelling factstore.Facts through Adapter.Assert.
func seedFleetThroughAdapter(t *testing.T, c *adapterpkg.Client) {
	t.Helper()
	a := adapterpkg.New(c)
	if err := a.Assert(context.Background(), []factstore.Fact{
		{RecordID: "501", Attribute: ":record/type", Value: "item"},
		{RecordID: "501", Attribute: ":record/status", Value: "active"},
		{RecordID: "501", Attribute: ":attr/km", Value: 45000.0},
		{RecordID: "502", Attribute: ":record/type", Value: "item"},
		{RecordID: "502", Attribute: ":record/status", Value: "retired"},
		{RecordID: "502", Attribute: ":attr/km", Value: 99999.0},
		{RecordID: "503", Attribute: ":record/type", Value: "item"},
		{RecordID: "503", Attribute: ":record/status", Value: "scheduled"},
		{RecordID: "503", Attribute: ":attr/km", Value: 10000.0},
		{RecordID: "601", Attribute: ":record/type", Value: "category"},
		{RecordID: "601", Attribute: ":record/name", Value: "Vehicles"},
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}
}

func TestServerQueryPatternOnly(t *testing.T) {
	t.Parallel()
	c, cleanup := dialServerQuery(t)
	defer cleanup()
	seedFleetThroughAdapter(t, c)

	rows, err := c.Query(context.Background(), adapterpkg.QueryInput{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3 (items): %v", len(rows), rows)
	}
}

func TestServerQueryPatternPlusPredicate(t *testing.T) {
	t.Parallel()
	c, cleanup := dialServerQuery(t)
	defer cleanup()
	seedFleetThroughAdapter(t, c)

	rows, err := c.Query(context.Background(), adapterpkg.QueryInput{
		Find: []string{"?e", "?km"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":attr/km", Value: factstore.Var("km")},
			&factstore.Predicate{Op: ">", Left: factstore.Var("km"), Right: factstore.Term{Literal: 20000.0}},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (km > 20000): %v", len(rows), rows)
	}
}

func TestServerQueryOr(t *testing.T) {
	t.Parallel()
	c, cleanup := dialServerQuery(t)
	defer cleanup()
	seedFleetThroughAdapter(t, c)

	rows, err := c.Query(context.Background(), adapterpkg.QueryInput{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Or{Branches: [][]factstore.Clause{
				{&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/status", Value: factstore.Term{Literal: "active"}}},
				{&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/status", Value: factstore.Term{Literal: "scheduled"}}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (active + scheduled): %v", len(rows), rows)
	}
}

func TestServerQueryNot(t *testing.T) {
	t.Parallel()
	c, cleanup := dialServerQuery(t)
	defer cleanup()
	seedFleetThroughAdapter(t, c)

	rows, err := c.Query(context.Background(), adapterpkg.QueryInput{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Not{Body: []factstore.Clause{
				&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/status", Value: factstore.Term{Literal: "retired"}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (active + scheduled): %v", len(rows), rows)
	}
}

func TestServerQueryFullText(t *testing.T) {
	t.Parallel()
	c, cleanup := dialServerQuery(t)
	defer cleanup()
	seedFleetThroughAdapter(t, c)

	rows, err := c.Query(context.Background(), adapterpkg.QueryInput{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "category"}},
			&factstore.FullText{Entity: factstore.Var("e"), Attribute: ":record/name", Query: "vehic"},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (Vehicles): %v", len(rows), rows)
	}
}

func TestServerQueryRejectsRules(t *testing.T) {
	t.Parallel()
	c, cleanup := dialServerQuery(t)
	defer cleanup()
	_, err := c.Query(context.Background(), adapterpkg.QueryInput{
		Find:  []string{"?e"},
		Rules: []factstore.Rule{{Name: "category-in-tree"}},
	})
	if err == nil {
		t.Fatal("expected ErrUnsupported for Rules (server-side composer)")
	}
}

func TestServerQueryRejectsUnknownAggregateFn(t *testing.T) {
	t.Parallel()
	c, cleanup := dialServerQuery(t)
	defer cleanup()
	_, err := c.Query(context.Background(), adapterpkg.QueryInput{
		Find:       []string{"?e"},
		Aggregates: []factstore.Aggregate{{Fn: "stddev"}}, // not implemented
	})
	if err == nil {
		t.Fatal("expected error for unknown aggregate function")
	}
}

func TestServerQuerySemanticsMatchAdapterClientSide(t *testing.T) {
	// Parity check: a Predicate+Pattern query returns the same rows
	// whether composed by the server (Client.Query) or by the
	// client-side Adapter.Query.
	t.Parallel()
	c, cleanup := dialServerQuery(t)
	defer cleanup()
	seedFleetThroughAdapter(t, c)

	q := adapterpkg.QueryInput{
		Find: []string{"?e", "?km"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":attr/km", Value: factstore.Var("km")},
			&factstore.Predicate{Op: ">", Left: factstore.Var("km"), Right: factstore.Term{Literal: 20000.0}},
		},
	}
	serverRows, err := c.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("server Query: %v", err)
	}
	adapter := adapterpkg.New(c)
	clientRows, err := adapter.Query(context.Background(), factstore.Query{Find: q.Find, Where: q.Where})
	if err != nil {
		t.Fatalf("adapter Query: %v", err)
	}
	if len(serverRows) != len(clientRows) {
		t.Fatalf("row count diverges: server=%d client=%d", len(serverRows), len(clientRows))
	}
}

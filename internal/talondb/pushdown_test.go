package talondb_test

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"sort"
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

// dialPushdownAdapter spins up the real bbolt + grpcserver so the
// pushdown tests exercise actual LookupNumericRange RPCs (not a
// mocked fake's stub).
func dialPushdownAdapter(t *testing.T) (*adapterpkg.Adapter, func()) {
	t.Helper()
	store, err := bboltstore.Open(filepath.Join(t.TempDir(), "push.bbolt"))
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
	c := adapterpkg.NewClientFromService(pb.NewTalonDBServiceClient(conn)).WithTenant("tenant-a")
	return adapterpkg.New(c), func() {
		_ = conn.Close()
		srv.GracefulStop()
		_ = store.Close()
	}
}

// seedKmFleet writes N items with sequential km values so range
// queries have something to scan.
func seedKmFleet(t *testing.T, a *adapterpkg.Adapter, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		if err := a.Assert(context.Background(), []factstore.Fact{
			{RecordID: fmt.Sprintf("d%03d", i), Attribute: ":record/type", Value: "item"},
			{RecordID: fmt.Sprintf("d%03d", i), Attribute: ":attr/km", Value: float64(i * 1000)},
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
}

func TestPushdownGreaterThan(t *testing.T) {
	t.Parallel()
	a, cleanup := dialPushdownAdapter(t)
	defer cleanup()
	seedKmFleet(t, a, 10)

	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e", "?km"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":attr/km", Value: factstore.Var("km")},
			&factstore.Predicate{Op: ">", Left: factstore.Var("km"), Right: factstore.Term{Literal: 5000.0}},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5 (km > 5000): %v", len(rows), rows)
	}
	for _, r := range rows {
		if r[1].(float64) <= 5000 {
			t.Errorf("row leaked km=%v", r[1])
		}
	}
}

func TestPushdownMultipleBoundsTighten(t *testing.T) {
	// 3000 < km < 7000 → items 4,5,6 only.
	t.Parallel()
	a, cleanup := dialPushdownAdapter(t)
	defer cleanup()
	seedKmFleet(t, a, 10)

	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":attr/km", Value: factstore.Var("km")},
			&factstore.Predicate{Op: ">", Left: factstore.Var("km"), Right: factstore.Term{Literal: 3000.0}},
			&factstore.Predicate{Op: "<", Left: factstore.Var("km"), Right: factstore.Term{Literal: 7000.0}},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3: %v", len(rows), rows)
	}
}

func TestPushdownEquals(t *testing.T) {
	t.Parallel()
	a, cleanup := dialPushdownAdapter(t)
	defer cleanup()
	seedKmFleet(t, a, 10)

	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":attr/km", Value: factstore.Var("km")},
			&factstore.Predicate{Op: "==", Left: factstore.Var("km"), Right: factstore.Term{Literal: 5000.0}},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: %v", len(rows), rows)
	}
}

func TestPushdownLiteralOnLeftFlipsOp(t *testing.T) {
	// 5000 < ?km is equivalent to ?km > 5000.
	t.Parallel()
	a, cleanup := dialPushdownAdapter(t)
	defer cleanup()
	seedKmFleet(t, a, 10)

	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":attr/km", Value: factstore.Var("km")},
			&factstore.Predicate{Op: "<", Left: factstore.Term{Literal: 5000.0}, Right: factstore.Var("km")},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5: %v", len(rows), rows)
	}
}

func TestPushdownNotEqualNotPushed(t *testing.T) {
	// != is not pushed down — the LookupNumericRange wire surface
	// doesn't express "not in [a,b]". Query must still produce the
	// right result via Go-side post-filter.
	t.Parallel()
	a, cleanup := dialPushdownAdapter(t)
	defer cleanup()
	seedKmFleet(t, a, 5)

	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":attr/km", Value: factstore.Var("km")},
			&factstore.Predicate{Op: "!=", Left: factstore.Var("km"), Right: factstore.Term{Literal: 3000.0}},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4 (all except 3000): %v", len(rows), rows)
	}
}

func TestPushdownStringComparisonNotPushed(t *testing.T) {
	// starts_with is a string op — must NOT be treated as a numeric
	// pushdown, even when it's on a var bound by a literal-attr
	// Pattern.
	t.Parallel()
	a, cleanup := dialPushdownAdapter(t)
	defer cleanup()
	if err := a.Assert(context.Background(), []factstore.Fact{
		{RecordID: "1", Attribute: ":record/type", Value: "item"},
		{RecordID: "1", Attribute: ":record/name", Value: "Truck A"},
		{RecordID: "2", Attribute: ":record/type", Value: "item"},
		{RecordID: "2", Attribute: ":record/name", Value: "Van B"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/name", Value: factstore.Var("n")},
			&factstore.Predicate{Op: "starts_with", Left: factstore.Var("n"), Right: factstore.Term{Literal: "Truck"}},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (Truck A): %v", len(rows), rows)
	}
}

func TestPushdownUnsatisfiableBoundsShortCircuit(t *testing.T) {
	// ?km > 5000 AND ?km < 1000 → no candidate can match. Adapter
	// returns empty rows without any per-doc fetch.
	t.Parallel()
	a, cleanup := dialPushdownAdapter(t)
	defer cleanup()
	seedKmFleet(t, a, 10)

	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":attr/km", Value: factstore.Var("km")},
			&factstore.Predicate{Op: ">", Left: factstore.Var("km"), Right: factstore.Term{Literal: 5000.0}},
			&factstore.Predicate{Op: "<", Left: factstore.Var("km"), Right: factstore.Term{Literal: 1000.0}},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected empty result, got %v", rows)
	}
}

func TestPushdownParityWith100Docs(t *testing.T) {
	// Brute-force-vs-pushdown parity: results match what a naive
	// Go-only post-filter would return.
	t.Parallel()
	a, cleanup := dialPushdownAdapter(t)
	defer cleanup()
	seedKmFleet(t, a, 100)

	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e", "?km"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":attr/km", Value: factstore.Var("km")},
			&factstore.Predicate{Op: ">=", Left: factstore.Var("km"), Right: factstore.Term{Literal: 20000.0}},
			&factstore.Predicate{Op: "<=", Left: factstore.Var("km"), Right: factstore.Term{Literal: 80000.0}},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// Expect items 20..80 → 61 rows.
	if len(rows) != 61 {
		t.Fatalf("got %d rows, want 61 (km in [20000, 80000])", len(rows))
	}
	var kms []float64
	for _, r := range rows {
		kms = append(kms, r[1].(float64))
	}
	sort.Float64s(kms)
	if kms[0] != 20000 || kms[len(kms)-1] != 80000 {
		t.Fatalf("bounds wrong: lo=%v hi=%v", kms[0], kms[len(kms)-1])
	}
}

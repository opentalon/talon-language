package talondb_test

import (
	"context"
	"fmt"
	"math"
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

// dialAggregateAdapter wires real bbolt + grpcserver via bufconn so
// every aggregate test exercises the full path including value
// binding from JSON.
func dialAggregateAdapter(t *testing.T) (*adapterpkg.Adapter, func()) {
	t.Helper()
	store, err := bboltstore.Open(filepath.Join(t.TempDir(), "agg.bbolt"))
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
	client := adapterpkg.NewClientFromService(pb.NewTalonDBServiceClient(conn)).WithTenant("tenant-a")
	a := adapterpkg.New(client)
	return a, func() {
		_ = conn.Close()
		srv.GracefulStop()
		_ = store.Close()
	}
}

// seedItems writes N item records of one of three statuses with a
// numeric :attr/km value. Returns the inputs so tests can compute
// expected aggregates without re-deriving them.
type item struct {
	id     string
	status string
	km     float64
}

func seedItems(t *testing.T, a *adapterpkg.Adapter, items []item) {
	t.Helper()
	for _, it := range items {
		if err := a.Assert(context.Background(), []factstore.Fact{
			{RecordID: it.id, Attribute: ":record/type", Value: "item"},
			{RecordID: it.id, Attribute: ":record/status", Value: it.status},
			{RecordID: it.id, Attribute: ":attr/km", Value: it.km},
		}); err != nil {
			t.Fatalf("seed %s: %v", it.id, err)
		}
	}
}

func TestAggregateCount(t *testing.T) {
	t.Parallel()
	a, cleanup := dialAggregateAdapter(t)
	defer cleanup()
	seedItems(t, a, []item{
		{"1", "active", 100}, {"2", "active", 200}, {"3", "retired", 300},
	})
	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
		},
		Aggregates: []factstore.Aggregate{{Fn: "count", Over: factstore.Var("e"), As: "n"}},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 || rows[0][0].(float64) != 3 {
		t.Fatalf("count = %v, want 3", rows)
	}
}

func TestAggregateSumAvgMinMax(t *testing.T) {
	t.Parallel()
	a, cleanup := dialAggregateAdapter(t)
	defer cleanup()
	seedItems(t, a, []item{
		{"1", "active", 10}, {"2", "active", 20}, {"3", "active", 30},
		{"4", "active", 40}, {"5", "active", 50},
	})
	q := factstore.Query{
		Find: []string{"?e", "?km"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":attr/km", Value: factstore.Var("km")},
		},
		Aggregates: []factstore.Aggregate{
			{Fn: "sum", Over: factstore.Var("km"), As: "total"},
			{Fn: "avg", Over: factstore.Var("km"), As: "mean"},
			{Fn: "min", Over: factstore.Var("km"), As: "lo"},
			{Fn: "max", Over: factstore.Var("km"), As: "hi"},
		},
	}
	rows, err := a.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 || len(rows[0]) != 4 {
		t.Fatalf("unexpected row shape %v", rows)
	}
	if got := rows[0][0].(float64); got != 150 {
		t.Errorf("sum = %v, want 150", got)
	}
	if got := rows[0][1].(float64); math.Abs(got-30) > 1e-9 {
		t.Errorf("avg = %v, want 30", got)
	}
	if got := rows[0][2].(float64); got != 10 {
		t.Errorf("min = %v, want 10", got)
	}
	if got := rows[0][3].(float64); got != 50 {
		t.Errorf("max = %v, want 50", got)
	}
}

func TestAggregateWithGroupBy(t *testing.T) {
	t.Parallel()
	a, cleanup := dialAggregateAdapter(t)
	defer cleanup()
	seedItems(t, a, []item{
		{"1", "active", 10}, {"2", "active", 20}, {"3", "active", 30},
		{"4", "retired", 100}, {"5", "retired", 200},
		{"6", "scheduled", 5},
	})
	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?status"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/status", Value: factstore.Var("status")},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":attr/km", Value: factstore.Var("km")},
		},
		GroupBy: []string{"?status"},
		Aggregates: []factstore.Aggregate{
			{Fn: "count", Over: factstore.Var("e"), As: "n"},
			{Fn: "sum", Over: factstore.Var("km"), As: "total"},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3 (one per status): %v", len(rows), rows)
	}
	// Sort rows by status so the assertions are stable irrespective of
	// adapter internal ordering.
	sort.Slice(rows, func(i, j int) bool {
		return fmt.Sprint(rows[i][0]) < fmt.Sprint(rows[j][0])
	})
	want := []struct {
		status   string
		count    float64
		totalKm  float64
	}{
		{"active", 3, 60},
		{"retired", 2, 300},
		{"scheduled", 1, 5},
	}
	for i, w := range want {
		if rows[i][0].(string) != w.status {
			t.Errorf("row[%d] status = %v, want %v", i, rows[i][0], w.status)
		}
		if rows[i][1].(float64) != w.count {
			t.Errorf("row[%d] count = %v, want %v", i, rows[i][1], w.count)
		}
		if rows[i][2].(float64) != w.totalKm {
			t.Errorf("row[%d] sum km = %v, want %v", i, rows[i][2], w.totalKm)
		}
	}
}

func TestAggregateEmptyMatchSetReturnsOneRowWithZeros(t *testing.T) {
	t.Parallel()
	a, cleanup := dialAggregateAdapter(t)
	defer cleanup()
	// Empty store; query matches nothing.
	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
		},
		Aggregates: []factstore.Aggregate{
			{Fn: "count", Over: factstore.Var("e"), As: "n"},
			{Fn: "sum", Over: factstore.Var("km"), As: "total"},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row for empty aggregate, got %v", rows)
	}
	if rows[0][0].(float64) != 0 {
		t.Errorf("count = %v, want 0", rows[0][0])
	}
	if rows[0][1].(float64) != 0 {
		t.Errorf("sum = %v, want 0", rows[0][1])
	}
}

func TestAggregate100DocsMatchesBruteForce(t *testing.T) {
	// Acceptance criterion from the issue: aggregate value matches a
	// brute-force computation over the same data.
	t.Parallel()
	a, cleanup := dialAggregateAdapter(t)
	defer cleanup()
	const n = 100
	var its []item
	var bruteSum float64
	for i := 1; i <= n; i++ {
		v := float64(i)
		its = append(its, item{id: fmt.Sprintf("d%03d", i), status: "active", km: v})
		bruteSum += v
	}
	seedItems(t, a, its)

	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e", "?km"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":attr/km", Value: factstore.Var("km")},
		},
		Aggregates: []factstore.Aggregate{
			{Fn: "count", Over: factstore.Var("e")},
			{Fn: "sum", Over: factstore.Var("km")},
			{Fn: "avg", Over: factstore.Var("km")},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if rows[0][0].(float64) != n {
		t.Errorf("count = %v, want %d", rows[0][0], n)
	}
	if rows[0][1].(float64) != bruteSum {
		t.Errorf("sum = %v, want %v", rows[0][1], bruteSum)
	}
	if got := rows[0][2].(float64); math.Abs(got-(bruteSum/float64(n))) > 1e-9 {
		t.Errorf("avg = %v, want %v", got, bruteSum/float64(n))
	}
}

func TestAggregateMinIgnoresNonNumeric(t *testing.T) {
	// min/max should silently skip non-numeric bindings rather than
	// erroring. Matches MemoryStore's tolerant behaviour.
	t.Parallel()
	a, cleanup := dialAggregateAdapter(t)
	defer cleanup()
	if err := a.Assert(context.Background(), []factstore.Fact{
		{RecordID: "1", Attribute: ":record/type", Value: "item"},
		{RecordID: "1", Attribute: ":attr/note", Value: "abc"},
		{RecordID: "2", Attribute: ":record/type", Value: "item"},
		{RecordID: "2", Attribute: ":attr/note", Value: 42.0},
		{RecordID: "3", Attribute: ":record/type", Value: "item"},
		{RecordID: "3", Attribute: ":attr/note", Value: 7.0},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e", "?n"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":attr/note", Value: factstore.Var("n")},
		},
		Aggregates: []factstore.Aggregate{
			{Fn: "min", Over: factstore.Var("n"), As: "lo"},
			{Fn: "max", Over: factstore.Var("n"), As: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if rows[0][0].(float64) != 7 {
		t.Errorf("min = %v, want 7", rows[0][0])
	}
	if rows[0][1].(float64) != 42 {
		t.Errorf("max = %v, want 42", rows[0][1])
	}
}

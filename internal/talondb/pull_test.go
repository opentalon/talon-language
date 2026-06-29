package talondb_test

import (
	"context"
	"net"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/opentalon/talon-language/internal/factstore"
	adapterpkg "github.com/opentalon/talon-language/internal/talondb"

	"github.com/opentalon/talon-db/bboltstore"
	"github.com/opentalon/talon-db/grpcserver"
	pb "github.com/opentalon/talon-db/proto/talondbpb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// dialPullAdapter sets up bbolt + grpcserver + adapter for real-wire
// Pull tests.
func dialPullAdapter(t *testing.T) (*adapterpkg.Adapter, func()) {
	t.Helper()
	store, err := bboltstore.Open(filepath.Join(t.TempDir(), "pull.bbolt"))
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

// seedPullFleet writes 3 item records with name + km attributes so
// pull queries have something to project from.
func seedPullFleet(t *testing.T, a *adapterpkg.Adapter) {
	t.Helper()
	for _, it := range []struct {
		id, name string
		km       float64
	}{
		{"501", "Truck A", 45000},
		{"502", "Van B", 25000},
		{"503", "Retired", 99000},
	} {
		if err := a.Assert(context.Background(), []factstore.Fact{
			{RecordID: it.id, Attribute: ":record/type", Value: "item"},
			{RecordID: it.id, Attribute: ":record/name", Value: it.name},
			{RecordID: it.id, Attribute: ":attr/km", Value: it.km},
		}); err != nil {
			t.Fatalf("seed %s: %v", it.id, err)
		}
	}
}

func TestPullFlatAttrList(t *testing.T) {
	t.Parallel()
	a, cleanup := dialPullAdapter(t)
	defer cleanup()
	seedPullFleet(t, a)

	rows, err := a.Query(context.Background(), factstore.Query{
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
		},
		Pull: []factstore.PullSpec{
			{EntityVar: "?e", Pattern: "[:record/name :attr/km]"},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3: %v", len(rows), rows)
	}
	var names []string
	for _, r := range rows {
		m := r[0].(map[string]any)
		// Only the requested attrs should be present.
		if len(m) != 2 {
			t.Errorf("projected map has %d keys, want 2: %v", len(m), m)
		}
		if _, ok := m[":record/type"]; ok {
			t.Errorf("non-requested :record/type leaked: %v", m)
		}
		names = append(names, m[":record/name"].(string))
	}
	sort.Strings(names)
	want := []string{"Retired", "Truck A", "Van B"}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("names[%d] = %q, want %q", i, names[i], want[i])
		}
	}
}

func TestPullWildcardReturnsWholeDoc(t *testing.T) {
	t.Parallel()
	a, cleanup := dialPullAdapter(t)
	defer cleanup()
	seedPullFleet(t, a)

	rows, err := a.Query(context.Background(), factstore.Query{
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/name", Value: factstore.Term{Literal: "Truck A"}},
		},
		Pull: []factstore.PullSpec{
			{EntityVar: "?e", Pattern: "[:*]"},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	m := rows[0][0].(map[string]any)
	for _, attr := range []string{":record/type", ":record/name", ":attr/km"} {
		if _, ok := m[attr]; !ok {
			t.Errorf("wildcard projection missing %q: %v", attr, m)
		}
	}
}

func TestPullMissingAttrOmittedFromProjection(t *testing.T) {
	t.Parallel()
	a, cleanup := dialPullAdapter(t)
	defer cleanup()
	seedPullFleet(t, a)

	rows, err := a.Query(context.Background(), factstore.Query{
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
		},
		Pull: []factstore.PullSpec{
			{EntityVar: "?e", Pattern: "[:record/name :attr/totally-missing]"},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	for _, r := range rows {
		m := r[0].(map[string]any)
		if _, ok := m[":attr/totally-missing"]; ok {
			t.Errorf("missing attr should be absent, got %v", m)
		}
		if _, ok := m[":record/name"]; !ok {
			t.Errorf("present attr should still be there: %v", m)
		}
	}
}

func TestPullMultipleSpecsProduceMultipleColumns(t *testing.T) {
	t.Parallel()
	a, cleanup := dialPullAdapter(t)
	defer cleanup()
	seedPullFleet(t, a)

	rows, err := a.Query(context.Background(), factstore.Query{
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/name", Value: factstore.Term{Literal: "Truck A"}},
		},
		Pull: []factstore.PullSpec{
			{EntityVar: "?e", Pattern: "[:record/name]"},
			{EntityVar: "?e", Pattern: "[:attr/km]"},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 || len(rows[0]) != 2 {
		t.Fatalf("expected single row, 2 columns: %v", rows)
	}
	col0 := rows[0][0].(map[string]any)
	col1 := rows[0][1].(map[string]any)
	if col0[":record/name"] != "Truck A" {
		t.Errorf("col0 = %v", col0)
	}
	if col1[":attr/km"].(float64) != 45000 {
		t.Errorf("col1 = %v", col1)
	}
}

func TestPullNestedRejected(t *testing.T) {
	t.Parallel()
	a, cleanup := dialPullAdapter(t)
	defer cleanup()
	seedPullFleet(t, a)

	_, err := a.Query(context.Background(), factstore.Query{
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
		},
		Pull: []factstore.PullSpec{
			{EntityVar: "?e", Pattern: "[:record/name {:friends 2}]"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "nested pull") {
		t.Fatalf("expected nested-pull rejection, got %v", err)
	}
}

func TestPullInvalidSyntaxRejected(t *testing.T) {
	t.Parallel()
	a, cleanup := dialPullAdapter(t)
	defer cleanup()
	seedPullFleet(t, a)

	for _, pat := range []string{
		"",
		"no brackets",
		"[",
		"[]",
	} {
		_, err := a.Query(context.Background(), factstore.Query{
			Where: []factstore.Clause{
				&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			},
			Pull: []factstore.PullSpec{{EntityVar: "?e", Pattern: pat}},
		})
		if err == nil {
			t.Errorf("pattern %q should error", pat)
		}
	}
}

func TestPullEntityVarOtherThanEntityRejected(t *testing.T) {
	t.Parallel()
	a, cleanup := dialPullAdapter(t)
	defer cleanup()
	seedPullFleet(t, a)

	_, err := a.Query(context.Background(), factstore.Query{
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
		},
		Pull: []factstore.PullSpec{
			{EntityVar: "?other", Pattern: "[:record/name]"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "?other") {
		t.Fatalf("expected EntityVar rejection, got %v", err)
	}
}

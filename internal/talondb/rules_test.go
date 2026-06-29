package talondb_test

import (
	"context"
	"net"
	"path/filepath"
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

// dialRules wires real bbolt → grpcserver → adapter so every test
// exercises the closure-table path end-to-end.
func dialRules(t *testing.T) (*adapterpkg.Adapter, pb.TalonDBServiceClient, func()) {
	t.Helper()
	store, err := bboltstore.Open(filepath.Join(t.TempDir(), "rules.bbolt"))
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
	raw := pb.NewTalonDBServiceClient(conn)
	client := adapterpkg.NewClientFromService(raw).WithTenant("tenant-a")
	a := adapterpkg.New(client)
	return a, raw, func() {
		_ = conn.Close()
		srv.GracefulStop()
		_ = store.Close()
	}
}

// putCategory writes a category record whose docID is its name (so
// closure-table parent pointers are by-name) plus the parent attribute
// linking up the tree.
func putCategory(t *testing.T, a *adapterpkg.Adapter, name, parent string) {
	t.Helper()
	facts := []factstore.Fact{
		{RecordID: name, Attribute: ":record/type", Value: "category"},
		{RecordID: name, Attribute: ":category/name", Value: name},
	}
	if parent != "" {
		facts = append(facts, factstore.Fact{
			RecordID: name, Attribute: "parent", Value: parent,
		})
	}
	if err := a.Assert(context.Background(), facts); err != nil {
		t.Fatalf("Assert %s: %v", name, err)
	}
}

// putItem assigns an item record to a category by name.
func putItem(t *testing.T, a *adapterpkg.Adapter, docID, categoryName string) {
	t.Helper()
	if err := a.Assert(context.Background(), []factstore.Fact{
		{RecordID: docID, Attribute: ":record/type", Value: "item"},
		{RecordID: docID, Attribute: ":record/category", Value: categoryName},
	}); err != nil {
		t.Fatalf("Assert item %s: %v", docID, err)
	}
}

// categoryInTreeRules is the canonical rule shape the adapter
// recognises. Matches what the Datalevin integration test uses.
func categoryInTreeRules() []factstore.Rule {
	return []factstore.Rule{
		{
			Name: "category-in-tree",
			Args: []string{"?c", "?root"},
			Body: []factstore.Clause{
				&factstore.Pattern{Entity: factstore.Var("cent"), Attribute: ":category/name", Value: factstore.Var("c")},
				&factstore.Predicate{Op: "=", Left: factstore.Var("c"), Right: factstore.Var("root")},
			},
		},
		{
			Name: "category-in-tree",
			Args: []string{"?c", "?root"},
			Body: []factstore.Clause{
				&factstore.Pattern{Entity: factstore.Var("cent"), Attribute: ":category/name", Value: factstore.Var("c")},
				&factstore.Pattern{Entity: factstore.Var("cent"), Attribute: ":category/parent", Value: factstore.Var("p")},
				&factstore.RuleCall{Name: "category-in-tree", Args: []factstore.Term{factstore.Var("p"), factstore.Var("root")}},
			},
		},
	}
}

func TestRuleCallThreeLevelTree(t *testing.T) {
	t.Parallel()
	a, _, cleanup := dialRules(t)
	defer cleanup()

	// Vehicles ← Trucks ← BigRigs
	putCategory(t, a, "Vehicles", "")
	putCategory(t, a, "Trucks", "Vehicles")
	putCategory(t, a, "BigRigs", "Trucks")

	putItem(t, a, "i1", "BigRigs")
	putItem(t, a, "i2", "Trucks")
	putItem(t, a, "i3", "Vehicles")
	putItem(t, a, "i4", "Buildings") // outside tree

	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/category", Value: factstore.Var("cat")},
			&factstore.RuleCall{Name: "category-in-tree", Args: []factstore.Term{factstore.Var("cat"), factstore.Term{Literal: "Vehicles"}}},
		},
		Rules: categoryInTreeRules(),
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3 (items in Vehicles tree): %v", len(rows), rows)
	}
}

func TestRuleCallBranchingTree(t *testing.T) {
	t.Parallel()
	a, _, cleanup := dialRules(t)
	defer cleanup()

	//          Vehicles
	//          /      \
	//      Trucks    Cars
	//        |
	//     BigRigs
	putCategory(t, a, "Vehicles", "")
	putCategory(t, a, "Trucks", "Vehicles")
	putCategory(t, a, "Cars", "Vehicles")
	putCategory(t, a, "BigRigs", "Trucks")

	putItem(t, a, "rig", "BigRigs")
	putItem(t, a, "sedan", "Cars")
	putItem(t, a, "tank", "Vehicles")

	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/category", Value: factstore.Var("cat")},
			&factstore.RuleCall{Name: "category-in-tree", Args: []factstore.Term{factstore.Var("cat"), factstore.Term{Literal: "Trucks"}}},
		},
		Rules: categoryInTreeRules(),
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (only BigRigs is under Trucks): %v", len(rows), rows)
	}
}

func TestRuleCallSingleNode(t *testing.T) {
	t.Parallel()
	a, _, cleanup := dialRules(t)
	defer cleanup()

	putCategory(t, a, "Vehicles", "")
	putItem(t, a, "v1", "Vehicles")
	putItem(t, a, "v2", "OtherCategory")

	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/category", Value: factstore.Var("cat")},
			&factstore.RuleCall{Name: "category-in-tree", Args: []factstore.Term{factstore.Var("cat"), factstore.Term{Literal: "Vehicles"}}},
		},
		Rules: categoryInTreeRules(),
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (v1 in Vehicles): %v", len(rows), rows)
	}
}

func TestRuleCallAbsentRoot(t *testing.T) {
	// Root category not present in store — only the literal root
	// itself is in the allowed set, but no item points at it.
	t.Parallel()
	a, _, cleanup := dialRules(t)
	defer cleanup()

	putItem(t, a, "v1", "Vehicles") // no such category written

	rows, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/category", Value: factstore.Var("cat")},
			&factstore.RuleCall{Name: "category-in-tree", Args: []factstore.Term{factstore.Var("cat"), factstore.Term{Literal: "Vehicles"}}},
		},
		Rules: categoryInTreeRules(),
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// Items with ?cat == "Vehicles" still pass — the literal root is
	// always in the allowed set.
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (literal-root match): %v", len(rows), rows)
	}
}

func TestRuleCallUnsupportedShape(t *testing.T) {
	t.Parallel()
	a, _, cleanup := dialRules(t)
	defer cleanup()
	putCategory(t, a, "Vehicles", "")
	putItem(t, a, "v1", "Vehicles")

	// Rule body doesn't reference :category/parent — not a recognised
	// shape.
	rules := []factstore.Rule{
		{Name: "bogus", Args: []string{"?c"}, Body: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Var("c")},
		}},
	}
	_, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.RuleCall{Name: "bogus", Args: []factstore.Term{factstore.Var("c"), factstore.Term{Literal: "X"}}},
		},
		Rules: rules,
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported-shape error, got %v", err)
	}
}

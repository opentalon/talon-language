package talondb_test

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/factstore"
	"github.com/opentalon/talon-language/internal/lexer"
	"github.com/opentalon/talon-language/internal/parser"
	adapterpkg "github.com/opentalon/talon-language/internal/talondb"

	"github.com/opentalon/talon-db/bboltstore"
	"github.com/opentalon/talon-db/grpcserver"
	pb "github.com/opentalon/talon-db/proto/talondbpb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// parseConstraintBlocks compiles a .tln source string and returns
// every ConstraintBlock it contained. Lets the constraint tests speak
// the real language instead of hand-rolling AST shapes.
func parseConstraintBlocks(t *testing.T, src string) []*ast.ConstraintBlock {
	t.Helper()
	tokens, ld := lexer.Lex("test.tln", src)
	if ld.HasErrors() {
		t.Fatalf("lex: %v", ld)
	}
	prog, pd := parser.Parse("test.tln", tokens)
	if pd.HasErrors() {
		t.Fatalf("parse: %v", pd)
	}
	var out []*ast.ConstraintBlock
	for _, b := range prog.Blocks {
		if cb, ok := b.(*ast.ConstraintBlock); ok {
			out = append(out, cb)
		}
	}
	if len(out) == 0 {
		t.Fatalf("source contained no constraint blocks")
	}
	return out
}

// dialAdapterForConstraints sets up bbolt + grpcserver + adapter so
// every test exercises the real Put / Get path through the gRPC
// surface, not a mocked one.
func dialAdapterForConstraints(t *testing.T) (*adapterpkg.Adapter, pb.TalonDBServiceClient, func()) {
	t.Helper()
	store, err := bboltstore.Open(filepath.Join(t.TempDir(), "cstr.bbolt"))
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
	adapter := adapterpkg.New(client)
	return adapter, raw, func() {
		_ = conn.Close()
		srv.GracefulStop()
		_ = store.Close()
	}
}

// raw helper: list every doc currently in the test tenant's docs
// bucket — read directly through gRPC.
func recordsInStore(t *testing.T, raw pb.TalonDBServiceClient, docIDs ...string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, id := range docIDs {
		resp, err := raw.Get(context.Background(), &pb.GetRequest{
			EntityId: "tenant-a", DocId: id,
		})
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if resp.GetFound() {
			out[id] = resp.GetDoc()
		}
	}
	return out
}

func TestAssertWithoutConstraintsPassesThrough(t *testing.T) {
	t.Parallel()
	a, raw, cleanup := dialAdapterForConstraints(t)
	defer cleanup()
	if err := a.Assert(context.Background(), []factstore.Fact{
		{RecordID: "501", Attribute: ":record/type", Value: "stock_item"},
		{RecordID: "501", Attribute: ":attr/current_stock", Value: 5.0},
	}); err != nil {
		t.Fatalf("Assert: %v", err)
	}
	got := recordsInStore(t, raw, "501")
	if len(got) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(got))
	}
}

func TestAssertWithConstraintAcceptsValidRecord(t *testing.T) {
	t.Parallel()
	a, raw, cleanup := dialAdapterForConstraints(t)
	defer cleanup()
	cs := parseConstraintBlocks(t, `
constraint "Stock cannot be negative" {
  for records where type == "stock_item"
  require attr "current_stock" >= 0
  on_violation reject "stock must be non-negative"
}`)
	gated := a.WithConstraints(cs)

	if err := gated.Assert(context.Background(), []factstore.Fact{
		{RecordID: "501", Attribute: ":record/type", Value: "stock_item"},
		{RecordID: "501", Attribute: ":attr/current_stock", Value: 5.0},
	}); err != nil {
		t.Fatalf("Assert valid record: %v", err)
	}
	if len(recordsInStore(t, raw, "501")) != 1 {
		t.Fatal("valid record should have been written")
	}
}

func TestAssertWithConstraintRejectsViolator(t *testing.T) {
	t.Parallel()
	a, raw, cleanup := dialAdapterForConstraints(t)
	defer cleanup()
	cs := parseConstraintBlocks(t, `
constraint "Stock cannot be negative" {
  for records where type == "stock_item"
  require attr "current_stock" >= 0
  on_violation reject "stock must be non-negative"
}`)
	gated := a.WithConstraints(cs)

	err := gated.Assert(context.Background(), []factstore.Fact{
		{RecordID: "501", Attribute: ":record/type", Value: "stock_item"},
		{RecordID: "501", Attribute: ":attr/current_stock", Value: -3.0},
	})
	var cve *adapterpkg.ConstraintViolationError
	if !errors.As(err, &cve) {
		t.Fatalf("expected ConstraintViolationError, got %v", err)
	}
	if cve.RecordID != "501" {
		t.Errorf("RecordID = %q, want 501", cve.RecordID)
	}
	if len(cve.Reasons) == 0 {
		t.Error("expected at least one reason")
	}
	// The Put RPC must NOT have been issued.
	if len(recordsInStore(t, raw, "501")) != 0 {
		t.Fatal("rejected record should not have been written")
	}
}

func TestAssertWithConstraintIgnoresMismatchedSelector(t *testing.T) {
	t.Parallel()
	a, raw, cleanup := dialAdapterForConstraints(t)
	defer cleanup()
	cs := parseConstraintBlocks(t, `
constraint "Stock cannot be negative" {
  for records where type == "stock_item"
  require attr "current_stock" >= 0
  on_violation reject "stock must be non-negative"
}`)
	gated := a.WithConstraints(cs)

	// Record is NOT a stock_item — constraint shouldn't apply.
	if err := gated.Assert(context.Background(), []factstore.Fact{
		{RecordID: "601", Attribute: ":record/type", Value: "category"},
		{RecordID: "601", Attribute: ":attr/current_stock", Value: -100.0},
	}); err != nil {
		t.Fatalf("Assert non-stock_item with bad stock: %v", err)
	}
	if len(recordsInStore(t, raw, "601")) != 1 {
		t.Fatal("non-matching selector shouldn't have blocked the write")
	}
}

func TestAssertWithConstraintWarnDoesNotBlock(t *testing.T) {
	t.Parallel()
	a, raw, cleanup := dialAdapterForConstraints(t)
	defer cleanup()
	cs := parseConstraintBlocks(t, `
constraint "Stock should not be negative" {
  for records where type == "stock_item"
  require attr "current_stock" >= 0
  on_violation warn "negative stock looks wrong"
}`)
	gated := a.WithConstraints(cs)

	if err := gated.Assert(context.Background(), []factstore.Fact{
		{RecordID: "501", Attribute: ":record/type", Value: "stock_item"},
		{RecordID: "501", Attribute: ":attr/current_stock", Value: -3.0},
	}); err != nil {
		t.Fatalf("warn-mode constraint should not block Assert, got %v", err)
	}
	if len(recordsInStore(t, raw, "501")) != 1 {
		t.Fatal("warn-mode write should have been persisted")
	}
}

func TestAssertConstraintEvaluatesMergedState(t *testing.T) {
	// Two-stage Assert: first sets type, second sets a negative stock.
	// The merged state at second-Assert time should still trigger the
	// constraint — proving Check sees the merged (existing + incoming)
	// record, not just the incoming facts.
	t.Parallel()
	a, raw, cleanup := dialAdapterForConstraints(t)
	defer cleanup()
	cs := parseConstraintBlocks(t, `
constraint "Stock cannot be negative" {
  for records where type == "stock_item"
  require attr "current_stock" >= 0
  on_violation reject "stock must be non-negative"
}`)
	gated := a.WithConstraints(cs)
	ctx := context.Background()

	// First write: type only — no stock yet, no violation.
	if err := gated.Assert(ctx, []factstore.Fact{
		{RecordID: "501", Attribute: ":record/type", Value: "stock_item"},
	}); err != nil {
		t.Fatalf("Assert type only: %v", err)
	}
	// Second write: introduce negative stock — merged state must be
	// {type, current_stock=-3}, constraint should fire.
	err := gated.Assert(ctx, []factstore.Fact{
		{RecordID: "501", Attribute: ":attr/current_stock", Value: -3.0},
	})
	var cve *adapterpkg.ConstraintViolationError
	if !errors.As(err, &cve) {
		t.Fatalf("merged-state violation: expected ConstraintViolationError, got %v", err)
	}
	// The doc should still exist from the first Assert, but the second
	// Assert's bad stock should NOT have been written.
	docs := recordsInStore(t, raw, "501")
	if len(docs) != 1 {
		t.Fatal("first Assert should have persisted the type")
	}
	if string(docs["501"]) == "" {
		t.Fatal("doc body empty")
	}
	// Cheaper than re-decoding: the bad value isn't present if the
	// body doesn't contain "current_stock".
	if contains(string(docs["501"]), "current_stock") {
		t.Errorf("rejected Assert leaked through: %s", docs["501"])
	}
}

func TestAssertConstraintIsImmutableAcrossClones(t *testing.T) {
	// WithConstraints returns a clone; the original adapter stays
	// constraint-free. Required so a single Client can serve both
	// gated and ungated writers (think: an admin tool bypassing
	// constraints intentionally).
	t.Parallel()
	a, _, cleanup := dialAdapterForConstraints(t)
	defer cleanup()
	cs := parseConstraintBlocks(t, `
constraint "Stock cannot be negative" {
  for records where type == "stock_item"
  require attr "current_stock" >= 0
  on_violation reject "no"
}`)
	gated := a.WithConstraints(cs)
	if err := gated.Assert(context.Background(), []factstore.Fact{
		{RecordID: "501", Attribute: ":record/type", Value: "stock_item"},
		{RecordID: "501", Attribute: ":attr/current_stock", Value: -3.0},
	}); err == nil {
		t.Fatal("gated adapter must reject")
	}
	if err := a.Assert(context.Background(), []factstore.Fact{
		{RecordID: "502", Attribute: ":record/type", Value: "stock_item"},
		{RecordID: "502", Attribute: ":attr/current_stock", Value: -3.0},
	}); err != nil {
		t.Fatalf("ungated original adapter unexpectedly rejected: %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

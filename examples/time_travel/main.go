// Command time_travel demonstrates the `was ( … ) N <unit> ago` time-travel
// detect condition running against a real talon-db backend.
//
// Time-travel needs facts written at different points in time. talon-db
// stamps its version history with the server clock, and that clock isn't
// controllable over the wire — so, unlike the REPL demos, this example
// embeds the real talon-db stack in-process (bbolt store + gRPC server +
// the FactStore adapter, over a Unix socket) and drives the store clock to
// backdate the "90 days ago" writes. Everything past the socket is the
// exact code that runs in production.
//
//	go run ./examples/time_travel
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "embed"

	"github.com/opentalon/tln-language/internal/factstore"
	"github.com/opentalon/tln-language/internal/talondb"
	"github.com/opentalon/tln-language/pkg/tln"

	"github.com/opentalon/talon-db/bboltstore"
	"github.com/opentalon/talon-db/grpcserver"
	pb "github.com/opentalon/talon-db/proto/talondbpb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

//go:embed certification.tln
var program string

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	// ── Bring up a real talon-db over a Unix socket ──────────────────────
	dir, err := os.MkdirTemp("", "tln-timetravel-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	store, err := bboltstore.Open(filepath.Join(dir, "demo.bbolt"))
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	sock := filepath.Join(dir, "talondb.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	srv := grpc.NewServer()
	pb.RegisterTalonDBServiceServer(srv, grpcserver.New(store, store.Events(), "demo"))
	go func() { _ = srv.Serve(lis) }()
	defer srv.GracefulStop()

	conn, err := grpc.NewClient("unix://"+sock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	adapter := talondb.New(talondb.NewClientFromService(pb.NewTalonDBServiceClient(conn)).WithTenant("fleet"))

	now := time.Now().UTC()
	names := map[int]string{1: "Excavator A", 2: "Loader B", 3: "Crane C", 4: "Forklift D"}

	// ── 90 days ago: every machine was certified (record 4 never was) ────
	store.SetClock(func() time.Time { return now.AddDate(0, 0, -90) })
	if err := adapter.Assert(ctx, []factstore.Fact{
		rec("1", "type", "machine"), rec("1", "status", "certified"), name("1", "Excavator A"),
		rec("2", "type", "machine"), rec("2", "status", "certified"), name("2", "Loader B"),
		rec("3", "type", "machine"), rec("3", "status", "certified"), name("3", "Crane C"),
		rec("4", "type", "machine"), rec("4", "status", "defective"), name("4", "Forklift D"),
	}); err != nil {
		return err
	}

	// ── Today: 2 and 3 have regressed to defective; 1 is still certified ─
	store.SetClock(func() time.Time { return now })
	if err := adapter.Assert(ctx, []factstore.Fact{
		rec("2", "status", "defective"),
		rec("3", "status", "defective"),
	}); err != nil {
		return err
	}

	// ── Run the detect against talon-db ──────────────────────────────────
	res, err := tln.Run(ctx, program, tln.WithFactStore(adapter))
	if err != nil {
		return err
	}

	block := res.Blocks["Certification regressed"]
	ids := make([]int, 0, len(block.Flagged))
	for _, row := range block.Flagged {
		if f, ok := row[0].(float64); ok {
			ids = append(ids, int(f))
		}
	}
	sort.Ints(ids)

	fmt.Printf("detect %q flagged %d machine(s) — defective now, certified 90 days ago:\n",
		"Certification regressed", len(ids))
	for _, id := range ids {
		fmt.Printf("  • %s (record %d)\n", names[id], id)
	}
	fmt.Println("\nnot flagged: Excavator A (still certified), Forklift D (never certified)")
	return nil
}

// rec builds a :record/<field> fact; name builds an :attr/name fact.
func rec(id, field, value string) factstore.Fact {
	return factstore.Fact{RecordID: id, Attribute: ":record/" + field, Value: value}
}

func name(id, value string) factstore.Fact {
	return factstore.Fact{RecordID: id, Attribute: ":attr/name", Value: value}
}

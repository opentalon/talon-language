package talondb_test

// Crash-recovery integration test for the adapter. Closes opentalon/
// talon-db#18.
//
// Strategy:
//
//  1. Build the real talondb-server binary into the test's tempdir
//     (sync.Once across the whole test package — one build, many tests).
//  2. Spawn it as a child process on a Unix socket, pointed at a
//     persistent bbolt file in another tempdir.
//  3. Drive the adapter through the wire: Assert N records.
//  4. SIGKILL the server (not SIGTERM — we want the unclean exit).
//  5. Verify the adapter's next call returns errors.Is(err, ErrUnavailable),
//     so callers can match the typed contract instead of scraping
//     strings.
//  6. Restart the server against the SAME db file, reconnect, and Query
//     — every record written in step 3 must come back.
//
// We deliberately reuse the talon-db crash_test.go pattern (subprocess
// + handshake line + SIGKILL) for parity. The novel claim being
// exercised here is the adapter half: that the gRPC client surfaces
// transport failures as the typed ErrUnavailable sentinel, and that a
// restart sees the on-disk state intact.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/opentalon/tln-language/internal/factstore"
	adapterpkg "github.com/opentalon/tln-language/internal/talondb"
)

var (
	buildOnce sync.Once
	builtBin  string
	buildErr  error
)

// buildTalondbServer compiles cmd/talondb-server from the module cache
// (or the local replace directive) once per test process. Subsequent
// callers reuse the binary.
func buildTalondbServer(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		bin := filepath.Join(os.TempDir(), fmt.Sprintf("talondb-server-%d", os.Getpid()))
		cmd := exec.Command("go", "build", "-o", bin,
			"github.com/opentalon/talon-db/cmd/talondb-server")
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			buildErr = fmt.Errorf("go build talondb-server: %w", err)
			return
		}
		builtBin = bin
	})
	if buildErr != nil {
		t.Fatalf("%v", buildErr)
	}
	return builtBin
}

type serverProc struct {
	cmd  *exec.Cmd
	addr string // host:port for TCP
}

// startServerTCP launches talondb-server with --db dbPath and
// --tcp 127.0.0.1:0, then parses the announced port from stdout. TCP
// (not Unix sockets) so the tests stay portable to macOS, whose
// sun_path limit makes the tempdir path too long.
func startServerTCP(t *testing.T, bin, dbPath string) *serverProc {
	t.Helper()
	cmd := exec.Command(bin, "--db", dbPath, "--tcp", "127.0.0.1:0")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start server: %v", err)
	}

	addrCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			const prefix = "talondb-server ready tcp://"
			if strings.HasPrefix(line, prefix) {
				select {
				case addrCh <- strings.TrimPrefix(line, prefix):
				default:
				}
			}
		}
		_, _ = io.Copy(io.Discard, stdout)
	}()

	var addr string
	select {
	case addr = <-addrCh:
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("server did not announce a TCP listener within 15s")
	}
	return &serverProc{cmd: cmd, addr: addr}
}

func (p *serverProc) kill(t *testing.T) {
	t.Helper()
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	if err := p.cmd.Process.Kill(); err != nil {
		t.Errorf("Kill: %v", err)
	}
	_ = p.cmd.Wait()
}

func (p *serverProc) gracefulStop() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	_ = p.cmd.Wait()
}

func skipIfShortOrWindows(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("subprocess crash test skipped in -short")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Unix-socket + SIGKILL semantics not portable to Windows")
	}
}

// TestAdapterCrashSurvivesAndRecovers covers the full lifecycle:
// Assert -> SIGKILL -> typed-error -> restart -> Query sees everything.
func TestAdapterCrashSurvivesAndRecovers(t *testing.T) {
	skipIfShortOrWindows(t)

	bin := buildTalondbServer(t)
	dbPath := filepath.Join(t.TempDir(), "tln.bbolt")

	srv := startServerTCP(t, bin, dbPath)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := adapterpkg.NewClient(ctx, srv.addr)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := client.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}
	a := adapterpkg.New(client.WithTenant("tenant-crash"))

	const want = 10
	for i := 1; i <= want; i++ {
		if err := a.Assert(ctx, []factstore.Fact{
			{RecordID: fmt.Sprintf("rec-%03d", i), Attribute: ":record/type", Value: "item"},
			{RecordID: fmt.Sprintf("rec-%03d", i), Attribute: ":attr/km", Value: float64(i * 1000)},
		}); err != nil {
			t.Fatalf("Assert %d: %v", i, err)
		}
	}

	srv.kill(t)

	// Next RPC against the dead server must be typed Unavailable so
	// callers can retry with backoff without scraping the message.
	err = a.Assert(ctx, []factstore.Fact{
		{RecordID: "post-kill", Attribute: ":record/type", Value: "item"},
	})
	if err == nil {
		t.Fatal("expected Assert against dead server to error")
	}
	if !errors.Is(err, adapterpkg.ErrUnavailable) {
		t.Errorf("post-SIGKILL Assert: want errors.Is(err, ErrUnavailable), got %v", err)
	}
	_ = client.Close()

	srv2 := startServerTCP(t, bin, dbPath)
	defer srv2.gracefulStop()

	client2, err := adapterpkg.NewClient(ctx, srv2.addr)
	if err != nil {
		t.Fatalf("NewClient 2: %v", err)
	}
	defer func() { _ = client2.Close() }()
	if err := client2.Health(ctx); err != nil {
		t.Fatalf("Health after restart: %v", err)
	}
	a2 := adapterpkg.New(client2.WithTenant("tenant-crash"))

	rows, err := a2.Query(ctx, factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{
				Entity:    factstore.Var("e"),
				Attribute: ":record/type",
				Value:     factstore.Term{Literal: "item"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Query after restart: %v", err)
	}
	if len(rows) != want {
		t.Fatalf("after restart: got %d rows, want %d", len(rows), want)
	}
}

// TestAdapterDeadSocketReturnsUnavailable hits the simpler case: the
// server never existed. Health (or any RPC) must surface
// errors.Is(err, ErrUnavailable) so the executor can decide between
// retry and giving up.
func TestAdapterDeadSocketReturnsUnavailable(t *testing.T) {
	skipIfShortOrWindows(t)

	dead := filepath.Join(t.TempDir(), "nonexistent.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// grpc.NewClient is lazy: it won't error here even with no listener.
	client, err := adapterpkg.NewClient(ctx, "unix://"+dead)
	if err != nil {
		t.Fatalf("NewClient (lazy dial should succeed): %v", err)
	}
	defer func() { _ = client.Close() }()

	err = client.Health(ctx)
	if err == nil {
		t.Fatal("expected Health against dead socket to error")
	}
	if !errors.Is(err, adapterpkg.ErrUnavailable) && !errors.Is(err, adapterpkg.ErrDeadlineExceeded) {
		t.Errorf("dead-socket Health: want ErrUnavailable or ErrDeadlineExceeded, got %v", err)
	}
}


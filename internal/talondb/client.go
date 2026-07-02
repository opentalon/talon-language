package talondb

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	talondb "github.com/opentalon/talon-db"
	pb "github.com/opentalon/talon-db/proto/talondbpb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Client is a thin gRPC wrapper around a talondb-server. It mirrors
// the shape of internal/datalevin.Client (NewClient, WithTenant,
// Close, Health) so the executor doesn't need backend-specific
// branching at the wiring layer.
type Client struct {
	conn   *grpc.ClientConn
	svc    pb.TalonDBServiceClient
	tenant string
}

// newClientWithService builds a Client around a pre-constructed
// service stub. Tests use this to inject a fake; production code goes
// through NewClient.
func newClientWithService(svc pb.TalonDBServiceClient) *Client {
	return &Client{svc: svc}
}

// NewClientFromService builds a Client around an existing gRPC service
// stub. Useful for end-to-end tests that wire their own gRPC server
// behind a bufconn listener instead of dialing a real socket. Owns
// nothing — the caller is responsible for the underlying connection.
func NewClientFromService(svc pb.TalonDBServiceClient) *Client {
	return newClientWithService(svc)
}

// NewClient dials a talondb-server. target is either
// "unix:///path/to/talondb.sock" or "host:port" for TCP. The
// caller must Close the client when done.
func NewClient(ctx context.Context, target string) (*Client, error) {
	dialTarget, dialer := parseTarget(target)
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	if dialer != nil {
		opts = append(opts, grpc.WithContextDialer(dialer))
	}
	conn, err := grpc.NewClient(dialTarget, opts...)
	if err != nil {
		return nil, fmt.Errorf("talondb: dial %q: %w", target, err)
	}
	return &Client{conn: conn, svc: pb.NewTalonDBServiceClient(conn)}, nil
}

// Close releases the gRPC connection.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// WithTenant returns a clone targeting a specific tenant (talondb
// entity_id). Empty Tenant maps to the "default" tenant.
func (c *Client) WithTenant(name string) *Client {
	clone := *c
	clone.tenant = name
	return &clone
}

// Tenant returns the entity_id this client will pass on every RPC.
// Returns "default" when no tenant was set.
func (c *Client) Tenant() string {
	if c.tenant == "" {
		return "default"
	}
	return c.tenant
}

// ClusterQuery asks the server to walk its temporal index for
// (entityID, itemID) and return non-overlapping clusters of events
// whose total span is at most `window` and whose size is at least
// `minSize`. `window=0` means "no upper bound". `types` filters by
// event type; empty means accept all. Mirrors
// talondb.IndexedStore.ClusterQuery via the gRPC wire.
func (c *Client) ClusterQuery(ctx context.Context, itemID string, types []string, window time.Duration, minSize int) ([]talondb.TemporalCluster, error) {
	resp, err := c.svc.ClusterQuery(ctx, &pb.ClusterQueryRequest{
		EntityId:    c.Tenant(),
		ItemId:      itemID,
		Types:       types,
		WindowNanos: window.Nanoseconds(),
		MinSize:     int32(minSize),
	})
	if err != nil {
		return nil, mapStatusError(err)
	}
	clusters := resp.GetClusters()
	out := make([]talondb.TemporalCluster, 0, len(clusters))
	for _, c := range clusters {
		events := make([]talondb.TemporalEvent, 0, len(c.GetEvents()))
		for _, e := range c.GetEvents() {
			events = append(events, talondb.TemporalEvent{
				DocID: e.GetDocId(),
				Type:  e.GetType(),
				At:    time.Unix(0, e.GetAtUnixNanos()),
			})
		}
		out = append(out, talondb.TemporalCluster{
			First:  time.Unix(0, c.GetFirstUnixNanos()),
			Last:   time.Unix(0, c.GetLastUnixNanos()),
			Events: events,
		})
	}
	return out, nil
}

// SequenceJoin asks the server to scan temporal indexes and return the
// items whose event log contains `steps` in order, with total span at
// most `window`. Empty `itemIDs` scans every item under the client's
// tenant. `window=0` means no upper bound on span. Mirrors
// talondb.IndexedStore.SequenceJoin via the gRPC wire.
func (c *Client) SequenceJoin(ctx context.Context, itemIDs, steps []string, window time.Duration) ([]talondb.SequenceMatch, error) {
	resp, err := c.svc.SequenceJoin(ctx, &pb.SequenceJoinRequest{
		EntityId:    c.Tenant(),
		ItemIds:     itemIDs,
		Steps:       steps,
		WindowNanos: window.Nanoseconds(),
	})
	if err != nil {
		return nil, mapStatusError(err)
	}
	matches := resp.GetMatches()
	out := make([]talondb.SequenceMatch, 0, len(matches))
	for _, m := range matches {
		events := make([]talondb.TemporalEvent, 0, len(m.GetEvents()))
		for _, e := range m.GetEvents() {
			events = append(events, talondb.TemporalEvent{
				DocID: e.GetDocId(),
				Type:  e.GetType(),
				At:    time.Unix(0, e.GetAtUnixNanos()),
			})
		}
		out = append(out, talondb.SequenceMatch{
			ItemID: m.GetItemId(),
			Events: events,
		})
	}
	return out, nil
}

// Query is the server-side equivalent of Adapter.Query: it translates
// a factstore.Query into a structured-query proto request, sends it
// to talondb-server, and decodes the rows back to [][]any in the same
// shape Adapter.Query already returns.
//
// Today Adapter.Query composes client-side; this is the wire that
// lets it delegate composition to the server in a future change.
// Supports Pattern, Predicate, Or, Not, FullText, plus Aggregates +
// GroupBy. Rules / Pull return errors.ErrUnsupported.
func (c *Client) Query(ctx context.Context, q QueryInput) ([][]any, error) {
	if len(q.Pull) > 0 || len(q.Rules) > 0 {
		return nil, errors.ErrUnsupported
	}
	clauses, err := encodeQueryClauses(q.Where)
	if err != nil {
		return nil, err
	}
	aggs, err := encodeQueryAggregates(q.Aggregates)
	if err != nil {
		return nil, err
	}
	resp, err := c.svc.Query(ctx, &pb.QueryRequest{
		EntityId:   c.Tenant(),
		Find:       q.Find,
		Where:      clauses,
		Aggregates: aggs,
		GroupBy:    q.GroupBy,
	})
	if err != nil {
		return nil, mapStatusError(err)
	}
	rows := resp.GetRows()
	out := make([][]any, 0, len(rows))
	for _, row := range rows {
		decoded := make([]any, 0, len(row.GetValues()))
		for _, v := range row.GetValues() {
			decoded = append(decoded, decodeStructValue(v))
		}
		out = append(out, decoded)
	}
	return out, nil
}

// LastWritten returns the updated_at time of the document (tenant,
// docID). ok is false when no such document exists. Doc-level
// granularity — every attribute of a record shares the document's
// last-write time.
func (c *Client) LastWritten(ctx context.Context, docID string) (time.Time, bool, error) {
	resp, err := c.svc.LastWritten(ctx, &pb.LastWrittenRequest{
		EntityId: c.Tenant(),
		DocId:    docID,
	})
	if err != nil {
		return time.Time{}, false, mapStatusError(err)
	}
	if !resp.GetFound() {
		return time.Time{}, false, nil
	}
	return time.Unix(0, resp.GetAtUnixNanos()).UTC(), true, nil
}

// Health pings the server. Returns an error if the RPC fails or the
// server reports a non-ok status.
func (c *Client) Health(ctx context.Context) error {
	resp, err := c.svc.Health(ctx, &emptypb.Empty{})
	if err != nil {
		return mapStatusError(err)
	}
	if resp.GetStatus() != "ok" {
		return fmt.Errorf("talondb: unhealthy: %s", resp.GetStatus())
	}
	return nil
}

// parseTarget converts the user-facing target string into the
// grpc-go dial form. For "unix:///path/to.sock" we return a custom
// dialer that uses net.Dial("unix", ...) because grpc-go's resolver
// has subtle quirks across versions; the explicit dialer is
// rock-solid.
func parseTarget(target string) (string, func(context.Context, string) (net.Conn, error)) {
	if strings.HasPrefix(target, "unix://") {
		path := strings.TrimPrefix(target, "unix://")
		return "unix:" + path, func(ctx context.Context, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "unix", path)
		}
	}
	return target, nil
}

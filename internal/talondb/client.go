package talondb

import (
	"context"
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
		return nil, err
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

// Health pings the server. Returns an error if the RPC fails or the
// server reports a non-ok status.
func (c *Client) Health(ctx context.Context) error {
	resp, err := c.svc.Health(ctx, &emptypb.Empty{})
	if err != nil {
		return err
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

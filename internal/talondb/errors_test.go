package talondb_test

import (
	"context"
	"errors"
	"testing"

	"github.com/opentalon/talon-language/internal/factstore"
	adapterpkg "github.com/opentalon/talon-language/internal/talondb"
	pb "github.com/opentalon/talon-db/proto/talondbpb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// erroringService returns a configurable gRPC status from every RPC.
// Tests construct one with the gRPC code they want to exercise and
// hand it to the Adapter via NewClientFromService.
type erroringService struct {
	pb.TalonDBServiceClient // embed for the methods we don't override
	code                    codes.Code
	msg                     string
}

func newErroringService(code codes.Code, msg string) *erroringService {
	return &erroringService{code: code, msg: msg}
}

func (e *erroringService) Put(ctx context.Context, _ *pb.PutRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Error(e.code, e.msg)
}
func (e *erroringService) Get(ctx context.Context, _ *pb.GetRequest, _ ...grpc.CallOption) (*pb.GetResponse, error) {
	return nil, status.Error(e.code, e.msg)
}
func (e *erroringService) Delete(ctx context.Context, _ *pb.DeleteRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	return nil, status.Error(e.code, e.msg)
}
func (e *erroringService) Lookup(ctx context.Context, _ *pb.LookupRequest, _ ...grpc.CallOption) (*pb.DocIDList, error) {
	return nil, status.Error(e.code, e.msg)
}

func newAdapterReturning(t *testing.T, code codes.Code) *adapterpkg.Adapter {
	t.Helper()
	svc := newErroringService(code, "test failure")
	return adapterpkg.New(adapterpkg.NewClientFromService(svc))
}

// ---------- Assert errors ----------

func TestAssertMapsNotFound(t *testing.T) {
	t.Parallel()
	a := newAdapterReturning(t, codes.NotFound)
	err := a.Assert(context.Background(), []factstore.Fact{
		{RecordID: "x", Attribute: ":a", Value: 1.0},
	})
	if !errors.Is(err, adapterpkg.ErrNotFound) {
		t.Fatalf("err = %v, want errors.Is(ErrNotFound)", err)
	}
}

func TestAssertMapsInvalidArgument(t *testing.T) {
	t.Parallel()
	a := newAdapterReturning(t, codes.InvalidArgument)
	err := a.Assert(context.Background(), []factstore.Fact{
		{RecordID: "x", Attribute: ":a", Value: 1.0},
	})
	if !errors.Is(err, adapterpkg.ErrInvalidArgument) {
		t.Fatalf("err = %v, want errors.Is(ErrInvalidArgument)", err)
	}
}

func TestAssertMapsUnavailable(t *testing.T) {
	t.Parallel()
	a := newAdapterReturning(t, codes.Unavailable)
	err := a.Assert(context.Background(), []factstore.Fact{
		{RecordID: "x", Attribute: ":a", Value: 1.0},
	})
	if !errors.Is(err, adapterpkg.ErrUnavailable) {
		t.Fatalf("err = %v, want errors.Is(ErrUnavailable)", err)
	}
}

func TestAssertMapsResourceExhausted(t *testing.T) {
	t.Parallel()
	a := newAdapterReturning(t, codes.ResourceExhausted)
	err := a.Assert(context.Background(), []factstore.Fact{
		{RecordID: "x", Attribute: ":a", Value: 1.0},
	})
	if !errors.Is(err, adapterpkg.ErrResourceExhausted) {
		t.Fatalf("err = %v, want errors.Is(ErrResourceExhausted)", err)
	}
}

func TestAssertMapsCanceled(t *testing.T) {
	t.Parallel()
	a := newAdapterReturning(t, codes.Canceled)
	err := a.Assert(context.Background(), []factstore.Fact{
		{RecordID: "x", Attribute: ":a", Value: 1.0},
	})
	if !errors.Is(err, adapterpkg.ErrCanceled) {
		t.Fatalf("err = %v, want errors.Is(ErrCanceled)", err)
	}
}

func TestAssertMapsDeadlineExceeded(t *testing.T) {
	t.Parallel()
	a := newAdapterReturning(t, codes.DeadlineExceeded)
	err := a.Assert(context.Background(), []factstore.Fact{
		{RecordID: "x", Attribute: ":a", Value: 1.0},
	})
	if !errors.Is(err, adapterpkg.ErrDeadlineExceeded) {
		t.Fatalf("err = %v, want errors.Is(ErrDeadlineExceeded)", err)
	}
}

func TestAssertMapsUnknownToInternal(t *testing.T) {
	t.Parallel()
	a := newAdapterReturning(t, codes.Unknown)
	err := a.Assert(context.Background(), []factstore.Fact{
		{RecordID: "x", Attribute: ":a", Value: 1.0},
	})
	if !errors.Is(err, adapterpkg.ErrInternal) {
		t.Fatalf("err = %v, want errors.Is(ErrInternal) for unmapped codes", err)
	}
}

// ---------- Retract errors ----------

func TestRetractMapsUnavailable(t *testing.T) {
	t.Parallel()
	a := newAdapterReturning(t, codes.Unavailable)
	err := a.Retract(context.Background(), factstore.RetractPattern{RecordID: "x"})
	if !errors.Is(err, adapterpkg.ErrUnavailable) {
		t.Fatalf("err = %v, want errors.Is(ErrUnavailable)", err)
	}
}

// ---------- Query errors ----------

func TestQueryLookupMapsUnavailable(t *testing.T) {
	t.Parallel()
	a := newAdapterReturning(t, codes.Unavailable)
	_, err := a.Query(context.Background(), factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
		},
	})
	if !errors.Is(err, adapterpkg.ErrUnavailable) {
		t.Fatalf("err = %v, want errors.Is(ErrUnavailable)", err)
	}
}

// ---------- Status preserved alongside sentinel ----------

// Joining a sentinel via errors.Join keeps the original gRPC error
// reachable via status.FromError, so callers can read the server-side
// message without losing the typed category.
func TestMappedErrorPreservesGRPCStatus(t *testing.T) {
	t.Parallel()
	a := newAdapterReturning(t, codes.NotFound)
	err := a.Assert(context.Background(), []factstore.Fact{
		{RecordID: "x", Attribute: ":a", Value: 1.0},
	})
	if !errors.Is(err, adapterpkg.ErrNotFound) {
		t.Fatalf("missing ErrNotFound: %v", err)
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("status.FromError lost the gRPC status: %v", err)
	}
	if st.Code() != codes.NotFound {
		t.Fatalf("status code = %v, want NotFound", st.Code())
	}
	if st.Message() != "test failure" {
		t.Fatalf("status message = %q, want %q", st.Message(), "test failure")
	}
}

// ---------- nil passthrough ----------

// Mapping a nil error must stay nil — otherwise every successful
// gRPC call would surface a sentinel as the err return.
func TestMapStatusErrorNilPassthrough(t *testing.T) {
	t.Parallel()
	// We exercise this through Retract on an adapter whose Delete
	// returns no error.
	svc := newPassThroughService()
	a := adapterpkg.New(adapterpkg.NewClientFromService(svc))
	if err := a.Retract(context.Background(), factstore.RetractPattern{RecordID: "x"}); err != nil {
		t.Fatalf("nil-pass-through Retract surfaced: %v", err)
	}
}

type passThroughService struct {
	pb.TalonDBServiceClient
}

func newPassThroughService() *passThroughService { return &passThroughService{} }

func (p *passThroughService) Delete(ctx context.Context, _ *pb.DeleteRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

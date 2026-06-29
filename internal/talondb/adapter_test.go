package talondb

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/opentalon/talon-language/internal/factstore"
	pb "github.com/opentalon/talon-db/proto/talondbpb"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

// fakeService is an in-memory implementation of
// pb.TalonDBServiceClient sufficient to exercise the adapter. Only
// the methods the adapter actually calls are real; everything else
// returns ErrUnsupported.
type fakeService struct {
	pb.TalonDBServiceClient // embed for forward-compat; nil methods panic
	docs                    map[string]map[string][]byte
	// terms[entity][term] -> sorted []docID
	terms map[string]map[string][]string
}

func newFakeService() *fakeService {
	return &fakeService{
		docs:  map[string]map[string][]byte{},
		terms: map[string]map[string][]string{},
	}
}

func (f *fakeService) Put(ctx context.Context, req *pb.PutRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	e := f.docs[req.GetEntityId()]
	if e == nil {
		e = map[string][]byte{}
		f.docs[req.GetEntityId()] = e
	}
	// Re-index: clear any prior contribution from this docID.
	if old, ok := e[req.GetDocId()]; ok {
		removeFromTerms(f.terms[req.GetEntityId()], req.GetDocId(), old)
	}
	e[req.GetDocId()] = append([]byte(nil), req.GetDoc()...)
	// Index every (key:value) composite term, matching the
	// extractor's `last_segment:value` rule. JSON top-level scalar
	// fields only — sufficient for the planner queries this adapter
	// emits.
	terms := extractTermsFromJSON(req.GetDoc())
	addToTerms(f.termsForEntity(req.GetEntityId()), req.GetDocId(), terms)
	return &emptypb.Empty{}, nil
}

func (f *fakeService) Get(ctx context.Context, req *pb.GetRequest, _ ...grpc.CallOption) (*pb.GetResponse, error) {
	if e, ok := f.docs[req.GetEntityId()]; ok {
		if doc, ok := e[req.GetDocId()]; ok {
			return &pb.GetResponse{Doc: doc, Found: true}, nil
		}
	}
	return &pb.GetResponse{Found: false}, nil
}

func (f *fakeService) Delete(ctx context.Context, req *pb.DeleteRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	if e, ok := f.docs[req.GetEntityId()]; ok {
		if old, ok := e[req.GetDocId()]; ok {
			removeFromTerms(f.terms[req.GetEntityId()], req.GetDocId(), old)
		}
		delete(e, req.GetDocId())
	}
	return &emptypb.Empty{}, nil
}

func (f *fakeService) Lookup(ctx context.Context, req *pb.LookupRequest, _ ...grpc.CallOption) (*pb.DocIDList, error) {
	if e, ok := f.terms[req.GetEntityId()]; ok {
		if ids, ok := e[req.GetTerm()]; ok {
			cp := append([]string(nil), ids...)
			return &pb.DocIDList{DocIds: cp}, nil
		}
	}
	return &pb.DocIDList{}, nil
}

func (f *fakeService) termsForEntity(entity string) map[string][]string {
	if e, ok := f.terms[entity]; ok {
		return e
	}
	e := map[string][]string{}
	f.terms[entity] = e
	return e
}

func extractTermsFromJSON(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+":"+stringifyValue(v))
	}
	sort.Strings(out)
	return out
}

func addToTerms(idx map[string][]string, docID string, terms []string) {
	for _, term := range terms {
		idx[term] = append(idx[term], docID)
		sort.Strings(idx[term])
	}
}

func removeFromTerms(idx map[string][]string, docID string, doc []byte) {
	if idx == nil {
		return
	}
	for _, term := range extractTermsFromJSON(doc) {
		ids := idx[term]
		out := ids[:0]
		for _, id := range ids {
			if id != docID {
				out = append(out, id)
			}
		}
		if len(out) == 0 {
			delete(idx, term)
		} else {
			idx[term] = out
		}
	}
}

// ---------- tests ----------

func newTestAdapter() (*Adapter, *fakeService) {
	fake := newFakeService()
	client := newClientWithService(fake)
	return New(client), fake
}

func TestAdapterAssertGroupsByRecordID(t *testing.T) {
	t.Parallel()
	a, fake := newTestAdapter()
	err := a.Assert(context.Background(), []factstore.Fact{
		{RecordID: "501", Attribute: ":record/type", Value: "item"},
		{RecordID: "501", Attribute: ":attr/km", Value: 45000.0},
		{RecordID: "502", Attribute: ":record/type", Value: "item"},
	})
	if err != nil {
		t.Fatalf("Assert: %v", err)
	}
	if len(fake.docs["default"]) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(fake.docs["default"]))
	}
	var got map[string]any
	if err := json.Unmarshal(fake.docs["default"]["501"], &got); err != nil {
		t.Fatalf("decode 501: %v", err)
	}
	if got[":record/type"] != "item" {
		t.Fatalf("501 type = %v", got[":record/type"])
	}
	if got[":attr/km"].(float64) != 45000 {
		t.Fatalf("501 km = %v", got[":attr/km"])
	}
}

func TestAdapterAssertMergesExistingDoc(t *testing.T) {
	t.Parallel()
	a, fake := newTestAdapter()
	ctx := context.Background()
	if err := a.Assert(ctx, []factstore.Fact{
		{RecordID: "501", Attribute: ":record/type", Value: "item"},
	}); err != nil {
		t.Fatalf("Assert 1: %v", err)
	}
	if err := a.Assert(ctx, []factstore.Fact{
		{RecordID: "501", Attribute: ":attr/km", Value: 45000.0},
	}); err != nil {
		t.Fatalf("Assert 2: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(fake.docs["default"]["501"], &got)
	if got[":record/type"] != "item" || got[":attr/km"].(float64) != 45000 {
		t.Fatalf("merge lost: %v", got)
	}
}

func TestAdapterRetractDeletesEntity(t *testing.T) {
	t.Parallel()
	a, fake := newTestAdapter()
	ctx := context.Background()
	_ = a.Assert(ctx, []factstore.Fact{{RecordID: "501", Attribute: ":record/type", Value: "item"}})
	if err := a.Retract(ctx, factstore.RetractPattern{RecordID: "501"}); err != nil {
		t.Fatalf("Retract: %v", err)
	}
	if _, ok := fake.docs["default"]["501"]; ok {
		t.Fatal("Retract did not delete doc")
	}
}

func TestAdapterQueryByPattern(t *testing.T) {
	t.Parallel()
	a, _ := newTestAdapter()
	ctx := context.Background()
	_ = a.Assert(ctx, []factstore.Fact{
		{RecordID: "501", Attribute: ":record/type", Value: "item"},
		{RecordID: "501", Attribute: ":attr/km", Value: 45000.0},
		{RecordID: "502", Attribute: ":record/type", Value: "item"},
		{RecordID: "502", Attribute: ":attr/km", Value: 10000.0},
		{RecordID: "503", Attribute: ":record/type", Value: "category"},
	})
	rows, err := a.Query(ctx, factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %v", len(rows), rows)
	}
}

func TestAdapterQueryWithPredicate(t *testing.T) {
	t.Parallel()
	a, _ := newTestAdapter()
	ctx := context.Background()
	_ = a.Assert(ctx, []factstore.Fact{
		{RecordID: "501", Attribute: ":record/type", Value: "item"},
		{RecordID: "501", Attribute: ":attr/km", Value: 45000.0},
		{RecordID: "502", Attribute: ":record/type", Value: "item"},
		{RecordID: "502", Attribute: ":attr/km", Value: 10000.0},
	})
	rows, err := a.Query(ctx, factstore.Query{
		Find: []string{"?e", "?km"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":attr/km", Value: factstore.Var("km")},
			&factstore.Predicate{Op: ">", Left: factstore.Var("km"), Right: factstore.Term{Literal: 20000.0}},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: %v", len(rows), rows)
	}
}

// PullSpec is now supported — see pull_test.go for the positive
// end-to-end cases. We keep this small check that Pull + Aggregates
// is rejected, since the two are mutually exclusive per Datalog
// convention.
func TestAdapterRejectsPullPlusAggregates(t *testing.T) {
	t.Parallel()
	a, _ := newTestAdapter()
	_, err := a.Query(context.Background(), factstore.Query{
		Find:       []string{"?e"},
		Pull:       []factstore.PullSpec{{EntityVar: "?e", Pattern: "[:*]"}},
		Aggregates: []factstore.Aggregate{{Fn: "count", Over: factstore.Var("e")}},
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("Pull+Aggregates should be rejected, got %v", err)
	}
}

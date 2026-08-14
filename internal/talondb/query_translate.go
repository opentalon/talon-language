package talondb

import (
	"fmt"

	"github.com/opentalon/tln-language/internal/factstore"
	pb "github.com/opentalon/talon-db/proto/talondbpb"

	structpb "google.golang.org/protobuf/types/known/structpb"
)

// QueryInput is the subset of factstore.Query shape that Client.Query
// forwards to the server. Mirrors the FactStore fields the server-side
// composer actually consumes; Rules / Pull are out of scope and
// trigger errors.ErrUnsupported on the client side. Aggregates +
// GroupBy are forwarded.
type QueryInput struct {
	Find       []string
	Where      []factstore.Clause
	Aggregates []factstore.Aggregate
	GroupBy    []string
	Rules      []factstore.Rule
	Pull       []factstore.PullSpec
}

// QueryFromFactstore converts a factstore.Query into the adapter's
// QueryInput shape so callers can hand a planner-emitted query
// straight through Client.Query without manual unwrapping.
func QueryFromFactstore(q factstore.Query) QueryInput {
	return QueryInput{
		Find:       q.Find,
		Where:      q.Where,
		Aggregates: q.Aggregates,
		GroupBy:    q.GroupBy,
		Rules:      q.Rules,
		Pull:       q.Pull,
	}
}

func encodeQueryClauses(in []factstore.Clause) ([]*pb.Clause, error) {
	out := make([]*pb.Clause, 0, len(in))
	for _, c := range in {
		encoded, err := encodeQueryClause(c)
		if err != nil {
			return nil, err
		}
		out = append(out, encoded)
	}
	return out, nil
}

func encodeQueryClause(c factstore.Clause) (*pb.Clause, error) {
	switch x := c.(type) {
	case *factstore.Pattern:
		return &pb.Clause{Clause: &pb.Clause_Pattern{Pattern: &pb.Pattern{
			Entity:    encodeTerm(x.Entity),
			Attribute: x.Attribute,
			Value:     encodeTerm(x.Value),
		}}}, nil
	case *factstore.Predicate:
		return &pb.Clause{Clause: &pb.Clause_Predicate{Predicate: &pb.Predicate{
			Op:    x.Op,
			Left:  encodeTerm(x.Left),
			Right: encodeTerm(x.Right),
		}}}, nil
	case *factstore.Or:
		branches := make([]*pb.ClauseList, 0, len(x.Branches))
		for _, b := range x.Branches {
			inner, err := encodeQueryClauses(b)
			if err != nil {
				return nil, err
			}
			branches = append(branches, &pb.ClauseList{Clauses: inner})
		}
		return &pb.Clause{Clause: &pb.Clause_Or{Or: &pb.Or{Branches: branches}}}, nil
	case *factstore.Not:
		body, err := encodeQueryClauses(x.Body)
		if err != nil {
			return nil, err
		}
		return &pb.Clause{Clause: &pb.Clause_Not{Not: &pb.Not{Body: body}}}, nil
	case *factstore.FullText:
		return &pb.Clause{Clause: &pb.Clause_Fulltext{Fulltext: &pb.FullText{
			Entity:    encodeTerm(x.Entity),
			Query:     x.Query,
			Attribute: x.Attribute,
		}}}, nil
	case *factstore.RuleCall:
		return nil, fmt.Errorf("talondb: RuleCall clause not supported by server-side Query (use Adapter.Query)")
	}
	return nil, fmt.Errorf("talondb: unknown clause type %T", c)
}

// encodeQueryAggregates translates factstore.Aggregate values into
// the proto's Aggregate shape. Unknown function names are caught here
// rather than at the server so the round-trip surfaces typed errors
// before going over the wire.
func encodeQueryAggregates(in []factstore.Aggregate) ([]*pb.Aggregate, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]*pb.Aggregate, 0, len(in))
	for _, a := range in {
		switch a.Fn {
		case "count", "sum", "total", "avg", "min", "max":
		default:
			return nil, fmt.Errorf("talondb: unknown aggregate function %q", a.Fn)
		}
		out = append(out, &pb.Aggregate{
			Fn:   a.Fn,
			Over: encodeTerm(a.Over),
			As:   a.As,
		})
	}
	return out, nil
}

func encodeTerm(t factstore.Term) *pb.Term {
	out := &pb.Term{}
	if t.Var != "" {
		out.Var = t.Var
	}
	if t.Literal != nil {
		v, err := structpb.NewValue(toStructpbInput(t.Literal))
		if err == nil {
			out.Literal = v
		}
	}
	return out
}

// toStructpbInput converts factstore literal values into types
// structpb.NewValue accepts (it doesn't handle int64 natively).
func toStructpbInput(v any) any {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case float32:
		return float64(x)
	}
	return v
}

func decodeStructValue(v *structpb.Value) any {
	if v == nil {
		return nil
	}
	switch k := v.GetKind().(type) {
	case *structpb.Value_NullValue:
		return nil
	case *structpb.Value_StringValue:
		return k.StringValue
	case *structpb.Value_NumberValue:
		return k.NumberValue
	case *structpb.Value_BoolValue:
		return k.BoolValue
	case *structpb.Value_ListValue:
		list := k.ListValue.GetValues()
		out := make([]any, 0, len(list))
		for _, e := range list {
			out = append(out, decodeStructValue(e))
		}
		return out
	case *structpb.Value_StructValue:
		m := map[string]any{}
		for kk, vv := range k.StructValue.GetFields() {
			m[kk] = decodeStructValue(vv)
		}
		return m
	}
	return nil
}

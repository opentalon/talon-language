package talondb_test

import (
	"context"
	"fmt"
	"math"
	"sort"
	"testing"

	"github.com/opentalon/tln-language/internal/factstore"
	adapterpkg "github.com/opentalon/tln-language/internal/talondb"
)

// TestServerQueryAggregateCount verifies count via Client.Query against
// a real bbolt + grpcserver. Mirrors the Adapter.Query test in
// aggregate_test.go but routes through the gRPC wire instead of the
// in-process composer.
func TestServerQueryAggregateCount(t *testing.T) {
	t.Parallel()
	c, cleanup := dialServerQuery(t)
	defer cleanup()
	seedFleetThroughAdapter(t, c)

	rows, err := c.Query(context.Background(), adapterpkg.QueryInput{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
		},
		Aggregates: []factstore.Aggregate{{Fn: "count", Over: factstore.Var("e"), As: "n"}},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 || rows[0][0].(float64) != 3 {
		t.Fatalf("count = %v, want 3", rows)
	}
}

func TestServerQueryAggregateSumAvgMinMax(t *testing.T) {
	t.Parallel()
	c, cleanup := dialServerQuery(t)
	defer cleanup()
	seedFleetThroughAdapter(t, c)

	rows, err := c.Query(context.Background(), adapterpkg.QueryInput{
		Find: []string{"?e", "?km"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":attr/km", Value: factstore.Var("km")},
		},
		Aggregates: []factstore.Aggregate{
			{Fn: "sum", Over: factstore.Var("km")},
			{Fn: "avg", Over: factstore.Var("km")},
			{Fn: "min", Over: factstore.Var("km")},
			{Fn: "max", Over: factstore.Var("km")},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// Fleet fixture km values: 45000, 99999, 10000.
	sum := rows[0][0].(float64)
	avg := rows[0][1].(float64)
	if sum != 45000+99999+10000 {
		t.Errorf("sum = %v, want %v", sum, 45000+99999+10000)
	}
	if math.Abs(avg-sum/3) > 1e-9 {
		t.Errorf("avg = %v, want %v", avg, sum/3)
	}
	if rows[0][2].(float64) != 10000 {
		t.Errorf("min = %v, want 10000", rows[0][2])
	}
	if rows[0][3].(float64) != 99999 {
		t.Errorf("max = %v, want 99999", rows[0][3])
	}
}

func TestServerQueryAggregateGroupBy(t *testing.T) {
	t.Parallel()
	c, cleanup := dialServerQuery(t)
	defer cleanup()
	seedFleetThroughAdapter(t, c)

	rows, err := c.Query(context.Background(), adapterpkg.QueryInput{
		Find: []string{"?status"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/status", Value: factstore.Var("status")},
		},
		GroupBy: []string{"?status"},
		Aggregates: []factstore.Aggregate{
			{Fn: "count", Over: factstore.Var("e")},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// Fleet fixture: active=1 (501), retired=1 (502), scheduled=1 (503).
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3 statuses: %v", len(rows), rows)
	}
	sort.Slice(rows, func(i, j int) bool {
		return fmt.Sprint(rows[i][0]) < fmt.Sprint(rows[j][0])
	})
	want := []struct {
		status string
		count  float64
	}{{"active", 1}, {"retired", 1}, {"scheduled", 1}}
	for i, w := range want {
		if rows[i][0].(string) != w.status {
			t.Errorf("row[%d] status = %v, want %v", i, rows[i][0], w.status)
		}
		if rows[i][1].(float64) != w.count {
			t.Errorf("row[%d] count = %v, want %v", i, rows[i][1], w.count)
		}
	}
}

func TestServerQueryAggregateEmptyMatchYieldsZeros(t *testing.T) {
	t.Parallel()
	c, cleanup := dialServerQuery(t)
	defer cleanup()
	// No seed — empty store.

	rows, err := c.Query(context.Background(), adapterpkg.QueryInput{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/type", Value: factstore.Term{Literal: "item"}},
		},
		Aggregates: []factstore.Aggregate{
			{Fn: "count", Over: factstore.Var("e")},
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 || rows[0][0].(float64) != 0 {
		t.Fatalf("empty count = %v, want 0", rows)
	}
}

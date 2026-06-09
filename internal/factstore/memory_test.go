package factstore

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func newSeeded(t *testing.T) *MemoryStore {
	t.Helper()
	m := NewMemoryStore()
	if err := m.Assert(context.Background(), []Fact{
		{RecordID: "501", Attribute: ":record/type", Value: "item"},
		{RecordID: "501", Attribute: ":record/status", Value: "active"},
		{RecordID: "501", Attribute: ":attr/km", Value: 45000.0},
		{RecordID: "501", Attribute: ":attr/name", Value: "VW Transporter"},

		{RecordID: "502", Attribute: ":record/type", Value: "item"},
		{RecordID: "502", Attribute: ":record/status", Value: "defective"},
		{RecordID: "502", Attribute: ":attr/km", Value: 10000.0},
		{RecordID: "502", Attribute: ":attr/name", Value: "Ford Transit"},

		{RecordID: "503", Attribute: ":record/type", Value: "person"},
		{RecordID: "503", Attribute: ":record/status", Value: "active"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return m
}

func TestAssertAndLen(t *testing.T) {
	m := newSeeded(t)
	if m.Len() != 3 {
		t.Errorf("Len: want 3, got %d", m.Len())
	}
}

func TestQueryPatternByAttributeAndLiteralValue(t *testing.T) {
	m := newSeeded(t)
	q := Query{
		Find: []string{"?e"},
		Where: []Clause{
			&Pattern{Entity: Var("e"), Attribute: ":record/type", Value: Lit("item")},
		},
	}
	rows, err := m.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("want 2 rows (501, 502), got %d (%v)", len(rows), rows)
	}
	for _, row := range rows {
		id := row[0].(float64)
		if id != 501 && id != 502 {
			t.Errorf("unexpected entity %v", id)
		}
	}
}

func TestQueryVariableBindingThenPredicate(t *testing.T) {
	m := newSeeded(t)
	// for type=item with km > 20000 — should match only 501.
	q := Query{
		Find: []string{"?e", "?km"},
		Where: []Clause{
			&Pattern{Entity: Var("e"), Attribute: ":record/type", Value: Lit("item")},
			&Pattern{Entity: Var("e"), Attribute: ":attr/km", Value: Var("km")},
			&Predicate{Op: ">", Left: Var("km"), Right: Lit(20000.0)},
		},
	}
	rows, err := m.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0][0].(float64) != 501 {
		t.Errorf("want entity 501, got %v", rows[0][0])
	}
	if rows[0][1].(float64) != 45000 {
		t.Errorf("want km=45000, got %v", rows[0][1])
	}
}

func TestQueryNot(t *testing.T) {
	m := newSeeded(t)
	// items NOT defective
	q := Query{
		Find: []string{"?e"},
		Where: []Clause{
			&Pattern{Entity: Var("e"), Attribute: ":record/type", Value: Lit("item")},
			&Not{Body: []Clause{
				&Pattern{Entity: Var("e"), Attribute: ":record/status", Value: Lit("defective")},
			}},
		},
	}
	rows, err := m.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 || rows[0][0].(float64) != 501 {
		t.Errorf("want only 501 active item, got %v", rows)
	}
}

func TestQueryOr(t *testing.T) {
	m := newSeeded(t)
	// type=item OR type=person
	q := Query{
		Find: []string{"?e"},
		Where: []Clause{
			&Or{Branches: [][]Clause{
				{&Pattern{Entity: Var("e"), Attribute: ":record/type", Value: Lit("item")}},
				{&Pattern{Entity: Var("e"), Attribute: ":record/type", Value: Lit("person")}},
			}},
		},
	}
	rows, err := m.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("want all three entities, got %v", rows)
	}
}

func TestQueryStringPredicate(t *testing.T) {
	m := newSeeded(t)
	q := Query{
		Find: []string{"?e", "?name"},
		Where: []Clause{
			&Pattern{Entity: Var("e"), Attribute: ":attr/name", Value: Var("name")},
			&Predicate{Op: "starts_with", Left: Var("name"), Right: Lit("VW")},
		},
	}
	rows, err := m.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 || rows[0][1] != "VW Transporter" {
		t.Errorf("want VW Transporter, got %v", rows)
	}
}

func TestAssertMutatesInPlace(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()
	if err := m.Assert(ctx, []Fact{
		{RecordID: "1", Attribute: ":record/type", Value: "x"},
	}); err != nil {
		t.Fatal(err)
	}
	// Re-assert different attribute on the same entity — merges, doesn't replace.
	if err := m.Assert(ctx, []Fact{
		{RecordID: "1", Attribute: ":attr/name", Value: "first"},
	}); err != nil {
		t.Fatal(err)
	}
	// Overwrite same attribute.
	if err := m.Assert(ctx, []Fact{
		{RecordID: "1", Attribute: ":attr/name", Value: "second"},
	}); err != nil {
		t.Fatal(err)
	}
	snap := m.Snapshot()
	want := map[int]map[string]any{
		1: {":record/type": "x", ":attr/name": "second"},
	}
	if !reflect.DeepEqual(snap, want) {
		t.Errorf("snapshot mismatch:\n got %v\nwant %v", snap, want)
	}
}

func TestAssertRejectsNonIntegerRecordID(t *testing.T) {
	m := NewMemoryStore()
	err := m.Assert(context.Background(), []Fact{
		{RecordID: "abc", Attribute: ":x", Value: 1},
	})
	if err == nil {
		t.Error("expected error for non-integer record ID")
	}
}

func TestResetClearsState(t *testing.T) {
	m := newSeeded(t)
	m.Reset()
	if m.Len() != 0 {
		t.Errorf("Reset did not clear; Len=%d", m.Len())
	}
}

func TestQueryOrderingDeterministic(t *testing.T) {
	m := newSeeded(t)
	q := Query{
		Find: []string{"?e"},
		Where: []Clause{
			&Pattern{Entity: Var("e"), Attribute: ":record/type", Value: Lit("item")},
		},
	}
	a, _ := m.Query(context.Background(), q)
	b, _ := m.Query(context.Background(), q)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("ordering not deterministic across runs:\n a=%v\n b=%v", a, b)
	}
}

// ─── Aggregation ──────────────────────────────────────────────────────────────

func TestQueryCount(t *testing.T) {
	m := newSeeded(t)
	q := Query{
		Where: []Clause{
			&Pattern{Entity: Var("e"), Attribute: ":record/type", Value: Lit("item")},
		},
		Aggregates: []Aggregate{{Fn: "count", Over: Var("e"), As: "n"}},
	}
	rows, err := m.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 || rows[0][0].(float64) != 2 {
		t.Errorf("expected count=2 over 2 item records, got %v", rows)
	}
}

func TestQuerySumAndAvg(t *testing.T) {
	m := newSeeded(t)
	q := Query{
		Where: []Clause{
			&Pattern{Entity: Var("e"), Attribute: ":record/type", Value: Lit("item")},
			&Pattern{Entity: Var("e"), Attribute: ":attr/km", Value: Var("km")},
		},
		Aggregates: []Aggregate{
			{Fn: "sum", Over: Var("km"), As: "total_km"},
			{Fn: "avg", Over: Var("km"), As: "avg_km"},
		},
	}
	rows, err := m.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 || rows[0][0].(float64) != 55000 || rows[0][1].(float64) != 27500 {
		// items 501 (km=45000) + 502 (km=10000) = 55000; avg = 27500.
		t.Errorf("want [55000 27500], got %v", rows)
	}
}

func TestQueryMinMax(t *testing.T) {
	m := newSeeded(t)
	q := Query{
		Where: []Clause{
			&Pattern{Entity: Var("e"), Attribute: ":attr/km", Value: Var("km")},
		},
		Aggregates: []Aggregate{
			{Fn: "min", Over: Var("km")},
			{Fn: "max", Over: Var("km")},
		},
	}
	rows, _ := m.Query(context.Background(), q)
	if len(rows) != 1 || rows[0][0].(float64) != 10000 || rows[0][1].(float64) != 45000 {
		t.Errorf("want [10000 45000], got %v", rows)
	}
}

func TestQueryGroupBy(t *testing.T) {
	m := newSeeded(t)
	q := Query{
		Where: []Clause{
			&Pattern{Entity: Var("e"), Attribute: ":record/status", Value: Var("status")},
		},
		GroupBy:    []string{"?status"},
		Aggregates: []Aggregate{{Fn: "count", Over: Var("e"), As: "n"}},
	}
	rows, err := m.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// 2 active (501, 503), 1 defective (502). Lex order: active, defective.
	want := [][]any{
		{"active", float64(2)},
		{"defective", float64(1)},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("group-by mismatch:\n got %v\nwant %v", rows, want)
	}
}

// Datalog string rendering for aggregate queries.
func TestQueryStringRendersAggregate(t *testing.T) {
	q := Query{
		Where: []Clause{
			&Pattern{Entity: Var("e"), Attribute: ":record/type", Value: Lit("item")},
			&Pattern{Entity: Var("e"), Attribute: ":attr/km", Value: Var("km")},
		},
		GroupBy:    []string{"?e"},
		Aggregates: []Aggregate{{Fn: "avg", Over: Var("km")}},
	}
	got := q.String()
	want := "[:find ?e (avg ?km)\n :where\n [?e :record/type \"item\"]\n [?e :attr/km ?km]]"
	if got != want {
		t.Errorf("render mismatch:\n got  %q\n want %q", got, want)
	}
}

func TestAggregateTotalAliasedToSum(t *testing.T) {
	q := Query{
		Aggregates: []Aggregate{{Fn: "total", Over: Var("x")}},
	}
	if !strings.Contains(q.String(), "(sum ?x)") {
		t.Errorf("expected (sum ?x), got %q", q.String())
	}
}

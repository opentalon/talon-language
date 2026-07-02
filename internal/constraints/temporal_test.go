package constraints

import (
	"testing"
	"time"

	"github.com/opentalon/talon-language/internal/ast"
)

var testNow = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

func TestEvalTodayComparison(t *testing.T) {
	cond := &ast.CompareCondition{
		Left: &ast.AttrExpr{Name: "d"}, Op: ">=", Right: &ast.TodayExpr{},
	}
	// A future date is >= today.
	if ok, err := EvalConditionAt(cond, map[string]any{"d": "2026-07-10"}, testNow); err != nil || !ok {
		t.Errorf("future >= today: ok=%v err=%v", ok, err)
	}
	// A past date is not >= today.
	if ok, err := EvalConditionAt(cond, map[string]any{"d": "2026-06-01"}, testNow); err != nil || ok {
		t.Errorf("past >= today: ok=%v err=%v", ok, err)
	}
}

func TestEvalApproachingBounds(t *testing.T) {
	// The desugared `attr "d" approaching within 7 days`.
	cond := &ast.LogicalCondition{
		Op:   "and",
		Left: &ast.CompareCondition{Left: &ast.AttrExpr{Name: "d"}, Op: ">=", Right: &ast.TodayExpr{}},
		Right: &ast.CompareCondition{
			Left: &ast.AttrExpr{Name: "d"}, Op: "<=",
			Right: &ast.BinaryExpr{Left: &ast.TodayExpr{}, Op: "+", Right: &ast.LiteralExpr{Value: ast.Duration{Value: 7, Unit: "days"}}},
		},
	}
	cases := []struct {
		date string
		want bool
	}{
		{"2026-07-04", true},  // 3 days out — approaching
		{"2026-07-01", true},  // today — in bounds
		{"2026-07-20", false}, // 19 days out — not within 7
		{"2026-06-25", false}, // past — not approaching
	}
	for _, c := range cases {
		got, err := EvalConditionAt(cond, map[string]any{"d": c.date}, testNow)
		if err != nil {
			t.Errorf("%s: err %v", c.date, err)
			continue
		}
		if got != c.want {
			t.Errorf("approaching %s: got %v, want %v", c.date, got, c.want)
		}
	}
}

func TestEvalOlderThan(t *testing.T) {
	cond := &ast.TemporalCondition{
		Subject: &ast.AttrExpr{Name: "seen"}, Op: "older_than", Value: ast.Duration{Value: 90, Unit: "days"},
	}
	if ok, _ := EvalConditionAt(cond, map[string]any{"seen": "2020-01-01"}, testNow); !ok {
		t.Error("2020 date should be older_than 90 days")
	}
	if ok, _ := EvalConditionAt(cond, map[string]any{"seen": "2026-06-20"}, testNow); ok {
		t.Error("recent date should not be older_than 90 days")
	}
}

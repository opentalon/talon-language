package constraints

import (
	"testing"
	"time"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/lexer"
	"github.com/opentalon/tln-language/internal/parser"
)

// evalStr parses a single expression (as the left side of a throwaway detect
// selector's compare) and evaluates it against the given row.
func evalStr(t *testing.T, expr string, row map[string]any) any {
	t.Helper()
	src := `detect "T" { for records where ` + expr + ` == "SENTINEL" }`
	tokens, ld := lexer.Lex("t.tln", src)
	if ld.HasErrors() {
		t.Fatalf("lex %q: %v", expr, ld)
	}
	prog, pd := parser.Parse("t.tln", tokens)
	if pd.HasErrors() {
		t.Fatalf("parse %q: %v", expr, pd)
	}
	det, ok := prog.Blocks[0].(*ast.DetectBlock)
	if !ok || len(det.Selector.Conditions) == 0 {
		t.Fatalf("parse %q: no selector condition", expr)
	}
	cmp, ok := det.Selector.Conditions[0].(*ast.CompareCondition)
	if !ok {
		t.Fatalf("parse %q: selector condition is %T, want compare", expr, det.Selector.Conditions[0])
	}
	v, err := EvalExpr(cmp.Left, row, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("eval %q: %v", expr, err)
	}
	return v
}

func TestStringBuiltins(t *testing.T) {
	row := map[string]any{
		"name": "  Broken Drill  ",
		"sku":  "AB1234",
		"vin":  "1ftabc",
		"n":    float64(3),
	}
	cases := []struct {
		expr string
		want any
	}{
		{`upper(attr "vin")`, "1FTABC"},
		{`lower(attr "sku")`, "ab1234"},
		{`trim(attr "name")`, "Broken Drill"},
		{`length(attr "sku")`, float64(6)},
		{`substring(attr "sku", 0, 2)`, "AB"},
		{`substring(attr "sku", 2)`, "1234"},
		{`replace(attr "sku", "AB", "XY")`, "XY1234"},
		{`concat("v-", attr "vin", "-", attr "n")`, "v-1ftabc-3"}, // number stringifies without .0
		{`join(split(attr "sku", "1"), "_")`, "AB_234"},           // split → ["AB","234"], join → "AB_234"
		{`upper(substring(attr "vin", 0, 3))`, "1FT"},             // nested
	}
	for _, c := range cases {
		got := evalStr(t, c.expr, row)
		if got != c.want {
			t.Errorf("%s = %#v, want %#v", c.expr, got, c.want)
		}
	}
}

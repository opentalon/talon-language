package gen_test

import (
	"fmt"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/gen"
)

// ExampleDetect shows the printer's primary use: turning a structured Go spec
// (here built by hand, but typically produced by a host-side discovery
// algorithm) into canonical .talon source that is guaranteed to re-parse.
func ExampleDetect() {
	high := ast.PriorityHigh
	label := ast.ParseTemplate("{item.name}: {attr.days_idle} days idle")

	block := &ast.DetectBlock{
		Name: "Idle high-value stock",
		Selector: ast.Selector{Conditions: []ast.Condition{
			&ast.LogicalCondition{
				Op:   "and",
				Left: &ast.CompareCondition{Left: &ast.IdentExpr{Name: "type"}, Op: "==", Right: &ast.LiteralExpr{Value: "stock_item"}},
				Right: &ast.CompareCondition{
					Left: &ast.AttrExpr{Name: "value"}, Op: ">", Right: &ast.LiteralExpr{Value: 1000.0},
				},
			},
		}},
		Flag:     &ast.FlagTarget{Kind: "items"},
		Label:    &label,
		Priority: &high,
	}

	fmt.Println(gen.Detect(block))
	// Output:
	// detect "Idle high-value stock" {
	//   for records where type == "stock_item" and attr "value" > 1000
	//   flag matching items
	//   label "{item.name}: {attr.days_idle} days idle"
	//   priority HIGH
	// }
}

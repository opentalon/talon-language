package validator

import (
	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/diagnostic"
)

// Validate checks the AST for semantic errors: undefined references, type
// mismatches, completeness, and cycle detection in define blocks.
func Validate(prog *ast.Program) diagnostic.List {
	panic("not implemented")
}

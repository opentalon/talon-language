package parser

import (
	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/diagnostic"
	"github.com/opentalon/talon-language/internal/lexer"
)

func Parse(file string, tokens []lexer.Token) (*ast.Program, diagnostic.List) {
	panic("not implemented")
}

package macro

import (
	"testing"

	"github.com/opentalon/tln-language/internal/ast"
)

// TestExpand_IdentityUntilGrammarLands pins the seam's current contract: with no
// `defmacro` grammar yet, Expand is the identity transform — it returns the same
// program and no diagnostics, so wiring it into the compile pipeline is a no-op.
// When the grammar lands (ADR 0011), this test is replaced by real
// expansion-fixpoint cases.
func TestExpand_IdentityUntilGrammarLands(t *testing.T) {
	prog := &ast.Program{Blocks: nil}
	out, diags := Expand("test.tln", prog)
	if diags.HasErrors() {
		t.Fatalf("identity expansion should not error, got %v", diags)
	}
	if out != prog {
		t.Fatalf("expected the same program back (identity), got a different pointer")
	}
}

package parser

import (
	"testing"

	"github.com/opentalon/tln-language/internal/ast"
	"github.com/opentalon/tln-language/internal/lexer"
)

// TestConnectorParsesWithEnvConfig checks a connector binds a name to a plugin
// and carries env-valued config.
func TestConnectorParsesWithEnvConfig(t *testing.T) {
	prog := mustParse(t, `connector "inventory" via mcp {
  endpoint env "INVENTORY_ENDPOINT"
  bearer env "INVENTORY_TOKEN"
}`)
	c := block[*ast.ConnectorBlock](t, prog, 0)
	if c.Name != "inventory" || c.Plugin != "mcp" {
		t.Fatalf("bad connector header: name=%q plugin=%q", c.Name, c.Plugin)
	}
	ev, ok := c.Config["bearer"].(*ast.EnvExpr)
	if !ok || ev.Name != "INVENTORY_TOKEN" {
		t.Fatalf("bearer should be env \"INVENTORY_TOKEN\", got %#v", c.Config["bearer"])
	}
	if _, ok := c.Config["endpoint"].(*ast.EnvExpr); !ok {
		t.Fatalf("endpoint should be an EnvExpr, got %#v", c.Config["endpoint"])
	}
}

// TestEnvRejectedOutsideConnector is the security boundary: `env` must not be
// usable in the general expression grammar, so an environment value can never
// flow into a label, a stored fact, or a tool argument (ADR 0012).
func TestEnvRejectedOutsideConnector(t *testing.T) {
	tokens, ld := lexer.Lex("t.tln", `detect "x" {
  for records where type == "v" and attr "a" == env "SECRET"
  flag matching items
}`)
	if ld.HasErrors() {
		t.Fatalf("unexpected lex errors: %v", ld)
	}
	_, pd := Parse("t.tln", tokens)
	if !pd.HasErrors() {
		t.Fatal("env used outside a connector must be a parse error, but parsed cleanly")
	}
}

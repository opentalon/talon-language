package mod

import "testing"

func TestParse_Basic(t *testing.T) {
	m, err := Parse(`
# my project's plugins
plugin "mcp" "v0.1.0"
plugin "io"  "v0.1.0"
plugin "db"  "v0.9.0" store
plugin "acme" "v1.2.0" from "github.com/acme/tln-acme"
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m.Plugins) != 4 {
		t.Fatalf("want 4 plugins, got %d", len(m.Plugins))
	}
	if got := m.Plugins[0]; got.Name != "mcp" || got.Version != "v0.1.0" || got.Module != "github.com/opentalon/tln-mcp" || got.Store {
		t.Errorf("mcp entry wrong: %+v", got)
	}
	if got := m.Plugins[1]; got.Module != "github.com/opentalon/tln-io" {
		t.Errorf("io should resolve to tln-io by convention, got %q", got.Module)
	}
	if got := m.Plugins[2]; !got.Store {
		t.Errorf("db should be a store plugin: %+v", got)
	}
	if got := m.Plugins[3]; got.Module != "github.com/acme/tln-acme" {
		t.Errorf("from-override wrong: %+v", got)
	}
}

func TestParse_Errors(t *testing.T) {
	for _, src := range []string{
		`plugin "mcp"`,              // missing version
		`plugin "mcp" "v1" "extra"`, // stray token
		`gem "mcp" "v1"`,            // wrong keyword
		`plugin "mcp" "v1"` + "\n" + `plugin "mcp" "v2"`, // duplicate
		`plugin "mcp" "v1" from`,                         // from without module
	} {
		if _, err := Parse(src); err == nil {
			t.Errorf("expected error for %q", src)
		}
	}
}

func TestParse_CommentsAndBlanks(t *testing.T) {
	m, err := Parse("// header\n\nplugin \"io\" \"v0.1.0\"   # inline\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m.Plugins) != 1 || m.Plugins[0].Name != "io" {
		t.Fatalf("want single io plugin, got %+v", m.Plugins)
	}
}

package main

import (
	"strings"
	"testing"

	"github.com/opentalon/tln-language/internal/mod"
)

func TestGenerateBundle_ToolPlugins(t *testing.T) {
	goMod, mainGo, lock := generateBundle([]mod.Plugin{
		{Name: "mcp", Version: "v0.1.0", Module: "github.com/opentalon/tln-mcp"},
		{Name: "io", Version: "v0.1.0", Module: "github.com/opentalon/io-tln"},
	})

	for _, want := range []string{
		"module tlnbundle",
		"github.com/opentalon/tln-language " + bundleTlnVersion,
		"github.com/opentalon/tln-mcp v0.1.0",
		"github.com/opentalon/io-tln v0.1.0",
	} {
		if !strings.Contains(goMod, want) {
			t.Errorf("go.mod missing %q\n---\n%s", want, goMod)
		}
	}
	for _, want := range []string{
		"DO NOT EDIT",
		`p0 "github.com/opentalon/tln-mcp"`,
		`p1 "github.com/opentalon/io-tln"`,
		`tln.WithPlugin("mcp", p0.Factory)`,
		`tln.WithPlugin("io", p1.Factory)`,
		"tln.WithEnv(os.LookupEnv)",
	} {
		if !strings.Contains(mainGo, want) {
			t.Errorf("main.go missing %q\n---\n%s", want, mainGo)
		}
	}
	if !strings.Contains(lock, "mcp github.com/opentalon/tln-mcp v0.1.0") {
		t.Errorf("mod.lock missing mcp entry:\n%s", lock)
	}
}

// TestGenerateBundle_SkipsStore: a store plugin is not compiled in (it has no
// tool Factory), so it must not appear in go.mod or main.go.
func TestGenerateBundle_SkipsStore(t *testing.T) {
	goMod, mainGo, _ := generateBundle([]mod.Plugin{
		{Name: "db", Version: "v0.1.0", Module: "github.com/opentalon/tln-db", Store: true},
	})
	if strings.Contains(goMod, "tln-db") || strings.Contains(mainGo, "tln-db") {
		t.Errorf("store plugin must not be bundled\n%s\n%s", goMod, mainGo)
	}
}

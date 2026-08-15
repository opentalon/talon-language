package main

import (
	"strings"
	"testing"

	"github.com/opentalon/tln-language/internal/mod"
)

func TestGenerateBundle_ToolPlugins(t *testing.T) {
	goMod, mainGo, lock, err := generateBundle([]mod.Plugin{
		{Name: "mcp", Version: "v0.1.0", Module: "github.com/opentalon/tln-mcp"},
		{Name: "io", Version: "v0.1.0", Module: "github.com/opentalon/tln-io"},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	for _, want := range []string{
		"module tlnbundle",
		"github.com/opentalon/tln-language " + bundleTlnVersion,
		"github.com/opentalon/tln-mcp v0.1.0",
		"github.com/opentalon/tln-io v0.1.0",
	} {
		if !strings.Contains(goMod, want) {
			t.Errorf("go.mod missing %q\n---\n%s", want, goMod)
		}
	}
	for _, want := range []string{
		"DO NOT EDIT",
		`p0 "github.com/opentalon/tln-mcp"`,
		`p1 "github.com/opentalon/tln-io"`,
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

// TestGenerateBundle_StorePlugin: a store plugin is imported and wired via
// LoadStoreConfig + WithFactStore, not WithPlugin.
func TestGenerateBundle_StorePlugin(t *testing.T) {
	_, mainGo, _, err := generateBundle([]mod.Plugin{
		{Name: "io", Version: "v0.1.0", Module: "github.com/opentalon/tln-io"},
		{Name: "db", Version: "v0.1.0", Module: "github.com/opentalon/tln-db", Store: true},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, want := range []string{
		`p1 "github.com/opentalon/tln-db"`,
		`tln.LoadStoreConfig("config/store.tln")`,
		`p1.Factory(spec)`,
		"tln.WithFactStore(store)",
	} {
		if !strings.Contains(mainGo, want) {
			t.Errorf("main.go missing store wiring %q\n---\n%s", want, mainGo)
		}
	}
	if strings.Contains(mainGo, `tln.WithPlugin("db"`) {
		t.Error("store plugin must not be registered via WithPlugin")
	}
}

// TestGenerateBundle_TwoStoresError: at most one store plugin.
func TestGenerateBundle_TwoStoresError(t *testing.T) {
	_, _, _, err := generateBundle([]mod.Plugin{
		{Name: "db", Version: "v0.1.0", Module: "github.com/opentalon/tln-db", Store: true},
		{Name: "dl", Version: "v0.1.0", Module: "github.com/opentalon/tln-dl", Store: true},
	})
	if err == nil {
		t.Fatal("expected an error for two store plugins")
	}
}

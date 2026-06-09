package imports

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/lexer"
	"github.com/opentalon/talon-language/internal/parser"
)

// writeFile writes a temp file and returns its full path.
func writeFile(t *testing.T, dir, name, src string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// parseFile reads + parses a single file (no import resolution).
func parseFile(t *testing.T, path string) *ast.Program {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	tokens, ld := lexer.Lex(filepath.Base(path), string(src))
	if ld.HasErrors() {
		t.Fatalf("lex: %v", ld)
	}
	prog, pd := parser.Parse(filepath.Base(path), tokens)
	if pd.HasErrors() {
		t.Fatalf("parse: %v", pd)
	}
	return prog
}

func TestResolveSingleImport(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shared.talon", `
define "active" {
  status == "active"
}
`)
	mainPath := writeFile(t, dir, "main.talon", `
import "./shared.talon"

detect "Live" {
  for records where type == "item" and is "active"
  flag matching items
}
`)
	prog := parseFile(t, mainPath)
	merged, diags := Resolve(prog, mainPath)
	if diags.HasErrors() {
		t.Fatalf("Resolve errors: %v", diags)
	}
	// Should have both the imported define and the local detect.
	if len(merged.Blocks) != 2 {
		t.Fatalf("want 2 blocks, got %d", len(merged.Blocks))
	}
	// Imported "active" define should land first (walk order).
	if _, ok := merged.Blocks[0].(*ast.DefineBlock); !ok {
		t.Errorf("first block should be the imported define, got %T", merged.Blocks[0])
	}
}

func TestResolveNestedImports(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "leaf.talon", `
define "leaf_def" {
  type == "leaf"
}
`)
	writeFile(t, dir, "middle.talon", `
import "./leaf.talon"

define "middle_def" {
  is "leaf_def"
}
`)
	mainPath := writeFile(t, dir, "main.talon", `
import "./middle.talon"

detect "All" {
  for records where is "middle_def"
  flag matching items
}
`)
	prog := parseFile(t, mainPath)
	merged, diags := Resolve(prog, mainPath)
	if diags.HasErrors() {
		t.Fatalf("Resolve errors: %v", diags)
	}
	if len(merged.Blocks) != 3 {
		t.Fatalf("want 3 blocks (leaf + middle + main), got %d", len(merged.Blocks))
	}
	// Depth-first: leaf is inlined before middle.
	names := make([]string, 0, 3)
	for _, b := range merged.Blocks {
		names = append(names, b.BlockName())
	}
	if names[0] != "leaf_def" || names[1] != "middle_def" || names[2] != "All" {
		t.Errorf("order: got %v, want [leaf_def middle_def All]", names)
	}
}

func TestResolveDetectsCycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.talon", `
import "./b.talon"

define "a_def" {
  type == "a"
}
`)
	writeFile(t, dir, "b.talon", `
import "./a.talon"

define "b_def" {
  type == "b"
}
`)
	mainPath := writeFile(t, dir, "main.talon", `
import "./a.talon"

detect "X" {
  for records where type == "x"
  flag matching items
}
`)
	prog := parseFile(t, mainPath)
	_, diags := Resolve(prog, mainPath)
	if !diags.HasErrors() {
		t.Fatal("expected cycle error")
	}
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "cycle") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("want a cycle diagnostic, got %v", diags)
	}
}

func TestResolveMissingFile(t *testing.T) {
	dir := t.TempDir()
	mainPath := writeFile(t, dir, "main.talon", `
import "./nope.talon"

detect "X" {
  for records where type == "item"
  flag matching items
}
`)
	prog := parseFile(t, mainPath)
	_, diags := Resolve(prog, mainPath)
	if !diags.HasErrors() {
		t.Fatal("expected error for missing file")
	}
	for _, d := range diags {
		if strings.Contains(d.Message, "cannot read") {
			return
		}
	}
	t.Errorf("want a 'cannot read' diagnostic, got %v", diags)
}

func TestResolveCallerShadowsImport(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shared.talon", `
define "active" {
  status == "active"
}
`)
	mainPath := writeFile(t, dir, "main.talon", `
import "./shared.talon"

define "active" {
  status == "running"
}

detect "X" {
  for records where is "active"
  flag matching items
}
`)
	prog := parseFile(t, mainPath)
	merged, diags := Resolve(prog, mainPath)
	if diags.HasErrors() {
		t.Fatalf("Resolve errors: %v", diags)
	}
	// Exactly 2 blocks (the caller's "active" wins, plus its detect).
	if len(merged.Blocks) != 2 {
		t.Fatalf("want 2 blocks after shadowing, got %d", len(merged.Blocks))
	}
}

func TestResolveSiblingConflictWarns(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "left.talon", `
define "shared" {
  type == "left"
}
`)
	writeFile(t, dir, "right.talon", `
define "shared" {
  type == "right"
}
`)
	mainPath := writeFile(t, dir, "main.talon", `
import "./left.talon"
import "./right.talon"

detect "X" {
  for records where type == "item"
  flag matching items
}
`)
	prog := parseFile(t, mainPath)
	merged, diags := Resolve(prog, mainPath)
	// The first-wins resolution should keep going (warning, not error).
	if diags.HasErrors() {
		t.Fatalf("conflicting imports should warn, not error: %v", diags)
	}
	hasWarn := false
	for _, d := range diags {
		if strings.Contains(d.Message, "conflicts") {
			hasWarn = true
		}
	}
	if !hasWarn {
		t.Errorf("want a conflict warning, got %v", diags)
	}
	// Two blocks (the surviving shared + the detect).
	if len(merged.Blocks) != 2 {
		t.Fatalf("want 2 blocks (first-wins), got %d", len(merged.Blocks))
	}
}

func TestResolveLateImportRejected(t *testing.T) {
	// `import` after a block — the parser already rejects this, but
	// the test pins the diagnostic so we notice if it ever stops.
	dir := t.TempDir()
	src := `
detect "X" {
  for records where type == "x"
  flag matching items
}

import "./other.talon"
`
	mainPath := writeFile(t, dir, "main.talon", src)
	srcBytes, _ := os.ReadFile(mainPath)
	tokens, _ := lexer.Lex("main.talon", string(srcBytes))
	_, pd := parser.Parse("main.talon", tokens)
	if !pd.HasErrors() {
		t.Fatal("expected parse error for late import")
	}
	found := false
	for _, d := range pd {
		if strings.Contains(d.Message, "before any block") {
			found = true
		}
	}
	if !found {
		t.Errorf("want a 'before any block' diagnostic, got %v", pd)
	}
}

func TestResolveNoImportsIsNoOp(t *testing.T) {
	src := `
detect "X" {
  for records where type == "item"
  flag matching items
}
`
	tokens, _ := lexer.Lex("test.talon", src)
	prog, _ := parser.Parse("test.talon", tokens)
	merged, diags := Resolve(prog, "test.talon")
	if diags.HasErrors() {
		t.Fatalf("no-import resolve should be clean: %v", diags)
	}
	if merged != prog {
		t.Error("no-imports path should return the same program pointer (no-op fast path)")
	}
}

// Package imports resolves `import "./path"` directives in a parsed
// Talon program. It recursively reads + parses each imported file, then
// merges the resulting blocks into the caller's program. Paths are
// always relative to the file that declared the import.
//
// This is the minimum viable module story for issue #19. No version
// resolution, no git fetching, no cache, no native plugins, no
// registry — just "merge this other file's blocks into my scope" so
// users can share `define` blocks (and any other top-level block)
// across files in the same project.
package imports

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/diagnostic"
	"github.com/opentalon/talon-language/internal/lexer"
	"github.com/opentalon/talon-language/internal/parser"
)

// Resolve walks the program's `import` directives, parses each target
// file recursively, and returns a new program whose Blocks slice
// contains the union of every imported file's blocks plus the
// caller's own. Imports themselves are dropped from the returned
// program — once merged they have no further runtime meaning.
//
// `basePath` is the path of the file that owns `prog`. Imports are
// resolved relative to its directory. An in-memory or REPL caller
// (no on-disk path) should pass "" and either avoid imports or use
// absolute paths.
//
// Diagnostics include the importing file's location so error
// messages stay actionable across a chain of imports. Cycle detection
// surfaces the import path that closed the loop.
//
// The returned program preserves block ordering: imported blocks
// appear before the caller's own, in the order their import
// statements were declared. Recursive imports are inlined depth-first
// so deeply-nested helpers land before the files that consume them.
func Resolve(prog *ast.Program, basePath string) (*ast.Program, diagnostic.List) {
	if len(prog.Imports) == 0 {
		return prog, nil
	}
	r := &resolver{
		seen: map[string]bool{},
	}
	if abs, err := filepath.Abs(basePath); err == nil {
		r.seen[abs] = true
	}
	var diags diagnostic.List
	importedBlocks, importDiags := r.walk(prog.Imports, basePath)
	diags = append(diags, importDiags...)

	// Caller blocks shadow imported ones on name conflict — the file
	// the user is actually editing wins. Conflicts among imports are
	// flagged so the user notices.
	merged := &ast.Program{
		Blocks: dedupeByName(importedBlocks, prog.Blocks, &diags, basePath),
	}
	return merged, diags
}

type resolver struct {
	// seen is keyed by absolute file path so cycles are detected
	// regardless of how a path is spelled (relative vs absolute,
	// `./a.talon` vs `a.talon`).
	seen map[string]bool
}

// walk loads each import recursively. `fromPath` is the file that
// owns the imports; relative paths resolve against its directory.
func (r *resolver) walk(imports []ast.ImportStatement, fromPath string) ([]ast.Block, diagnostic.List) {
	var out []ast.Block
	var diags diagnostic.List
	baseDir := filepath.Dir(fromPath)
	if fromPath == "" {
		baseDir = "."
	}

	for _, imp := range imports {
		target := imp.Path
		if !filepath.IsAbs(target) {
			target = filepath.Join(baseDir, target)
		}
		absTarget, err := filepath.Abs(target)
		if err != nil {
			diags.AddError(fromPath, imp.Pos.Line, imp.Pos.Col,
				fmt.Sprintf("cannot resolve import path %q: %v", imp.Path, err), "")
			continue
		}
		if r.seen[absTarget] {
			// Cycle — point at the offending import site so the user
			// can find the loop quickly.
			diags.AddError(fromPath, imp.Pos.Line, imp.Pos.Col,
				fmt.Sprintf("import cycle: %q has already been imported in this chain", imp.Path),
				"remove the duplicate import or split shared helpers into a third file")
			continue
		}
		r.seen[absTarget] = true

		src, err := os.ReadFile(absTarget)
		if err != nil {
			diags.AddError(fromPath, imp.Pos.Line, imp.Pos.Col,
				fmt.Sprintf("cannot read imported file %q: %v", imp.Path, err), "")
			continue
		}

		fileLabel := filepath.Base(absTarget)
		tokens, ld := lexer.Lex(fileLabel, string(src))
		diags = append(diags, ld...)
		if ld.HasErrors() {
			continue
		}
		subProg, pd := parser.Parse(fileLabel, tokens)
		diags = append(diags, pd...)
		if pd.HasErrors() {
			continue
		}

		// Recursively resolve the sub-program's own imports first.
		if len(subProg.Imports) > 0 {
			nested, nestedDiags := r.walk(subProg.Imports, absTarget)
			diags = append(diags, nestedDiags...)
			out = append(out, nested...)
		}
		out = append(out, subProg.Blocks...)
	}
	return out, diags
}

// dedupeByName resolves block-name collisions. Caller blocks always
// win (the file the user is editing is authoritative). Conflicts
// between two imports surface as warnings so the user can decide
// whether to rename or remove.
func dedupeByName(imported, caller []ast.Block, diags *diagnostic.List, basePath string) []ast.Block {
	byName := map[string]ast.Block{}
	for _, b := range imported {
		name := b.BlockName()
		if existing, ok := byName[name]; ok {
			// Two imports declared the same name. Keep the first; warn.
			diags.AddWarning(basePath, blockPos(b).Line, blockPos(b).Col,
				fmt.Sprintf("imported block %q conflicts with an earlier import; the first one wins", name),
				"rename one or remove the duplicate")
			_ = existing
			continue
		}
		byName[name] = b
	}
	// Caller blocks shadow imports.
	for _, b := range caller {
		byName[b.BlockName()] = b
	}

	// Preserve order: imports first (in walk order), then caller
	// blocks. Skip names the caller redefined.
	out := make([]ast.Block, 0, len(imported)+len(caller))
	callerNames := map[string]bool{}
	for _, b := range caller {
		callerNames[b.BlockName()] = true
	}
	seen := map[string]bool{}
	for _, b := range imported {
		name := b.BlockName()
		if callerNames[name] {
			continue // caller wins
		}
		if seen[name] {
			continue // first-import-wins on duplicates
		}
		seen[name] = true
		out = append(out, b)
	}
	out = append(out, caller...)
	return out
}

// blockPos returns a block's source position so diagnostic lines can
// be precise. Mirrors validator's same helper to avoid an import
// cycle (we live below validator in the dependency graph).
func blockPos(b ast.Block) ast.Pos {
	switch bb := b.(type) {
	case *ast.DetectBlock:
		return bb.Pos
	case *ast.RuleBlock:
		return bb.Pos
	case *ast.RecommendBlock:
		return bb.Pos
	case *ast.CombineBlock:
		return bb.Pos
	case *ast.DefineBlock:
		return bb.Pos
	case *ast.WorkflowBlock:
		return bb.Pos
	case *ast.PredictBlock:
		return bb.Pos
	case *ast.ForecastBlock:
		return bb.Pos
	case *ast.ClusterBlock:
		return bb.Pos
	case *ast.ClassifyBlock:
		return bb.Pos
	case *ast.SimilarBlock:
		return bb.Pos
	case *ast.RelatedBlock:
		return bb.Pos
	case *ast.OnBlock:
		return bb.Pos
	case *ast.ConstraintBlock:
		return bb.Pos
	}
	return ast.Pos{}
}

// shortPath is used in diagnostics to keep the noise tolerable.
// Returns the basename if the path is in the current working dir,
// otherwise returns the supplied path unchanged.
func shortPath(p string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return p
	}
	rel, err := filepath.Rel(cwd, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return p
	}
	return rel
}

var _ = shortPath // reserved for diagnostic phrasing; unused while errors carry full paths

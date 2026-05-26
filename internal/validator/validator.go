package validator

import (
	"fmt"
	"strings"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/diagnostic"
)

type validator struct {
	file    string
	prog    *ast.Program
	diags   diagnostic.List
	defines map[string]*ast.DefineBlock
	blocks  map[string]ast.Block // all named blocks
	seen    map[string]ast.Pos   // first occurrence, for duplicate detection
}

func Validate(file string, prog *ast.Program) diagnostic.List {
	v := &validator{
		file:    file,
		prog:    prog,
		defines: map[string]*ast.DefineBlock{},
		blocks:  map[string]ast.Block{},
		seen:    map[string]ast.Pos{},
	}
	v.collect()
	v.checkDuplicates()
	v.checkCompleteness()
	v.checkReferences()
	v.checkTypes()
	v.checkCycles()
	v.checkWorkflows()
	return v.diags
}

// ─── Collection ───────────────────────────────────────────────────────────────

func (v *validator) collect() {
	for _, b := range v.prog.Blocks {
		name := b.BlockName()
		if def, ok := b.(*ast.DefineBlock); ok {
			v.defines[name] = def
		}
		v.blocks[name] = b
	}
}

// ─── Duplicate names ──────────────────────────────────────────────────────────

func (v *validator) checkDuplicates() {
	for _, b := range v.prog.Blocks {
		name := b.BlockName()
		pos := blockPos(b)
		if prev, ok := v.seen[name]; ok {
			v.errAt(pos, fmt.Sprintf("duplicate block name %q (first defined at line %d)", name, prev.Line), "")
		} else {
			v.seen[name] = pos
		}
	}
}

// ─── Completeness ─────────────────────────────────────────────────────────────

func (v *validator) checkCompleteness() {
	for _, b := range v.prog.Blocks {
		switch bb := b.(type) {
		case *ast.DetectBlock:
			if bb.Flag == nil {
				v.errAt(bb.Pos, fmt.Sprintf("detect %q requires a 'flag' clause", bb.Name), "add: flag matching items")
			}
			if bb.Recommend != nil && bb.Recommend.Suggest == nil {
				v.errAt(bb.Recommend.Pos, fmt.Sprintf("nested recommend %q requires a 'suggest' clause", bb.Recommend.Name), "")
			}
		case *ast.RuleBlock:
			if bb.Block == nil && bb.Requires == nil && bb.Allow == nil {
				v.errAt(bb.Pos, fmt.Sprintf("rule %q requires 'block', 'allow', or 'requires' clause", bb.Name), "")
			}
		case *ast.RecommendBlock:
			if bb.Suggest == nil {
				v.errAt(bb.Pos, fmt.Sprintf("recommend %q requires a 'suggest' clause", bb.Name), "")
			}
		case *ast.PredictBlock:
			if len(bb.Features) == 0 {
				v.errAt(bb.Pos, fmt.Sprintf("predict %q requires a 'features' clause", bb.Name), "")
			}
		case *ast.ForecastBlock:
			if bb.Series.Attr == nil {
				v.errAt(bb.Pos, fmt.Sprintf("forecast %q requires a 'series' clause", bb.Name), "")
			}
		}
	}
}

// ─── Reference resolution ─────────────────────────────────────────────────────

func (v *validator) checkReferences() {
	defineNames := names(v.defines)
	blockNames := allNames(v.blocks)

	for _, b := range v.prog.Blocks {
		pos := blockPos(b)
		walkBlockConditions(b, func(cond ast.Condition) {
			switch c := cond.(type) {
			case *ast.IsCondition:
				if c.Name != "" {
					if _, ok := v.defines[c.Name]; !ok {
						v.errAt(pos,
							fmt.Sprintf("undefined define reference %q", c.Name),
							suggest(c.Name, defineNames))
					}
				}
			case *ast.BlockMatchesCondition:
				if _, ok := v.blocks[c.Name]; !ok {
					v.errAt(pos,
						fmt.Sprintf("undefined block reference %q", c.Name),
						suggest(c.Name, blockNames))
				}
			}
		})
	}
}

// ─── Type checking ────────────────────────────────────────────────────────────

func (v *validator) checkTypes() {
	for _, b := range v.prog.Blocks {
		pos := blockPos(b)
		walkBlockConditions(b, func(cond ast.Condition) {
			c, ok := cond.(*ast.CompareCondition)
			if !ok {
				return
			}
			switch c.Op {
			case ">", "<", ">=", "<=":
				if isStringLiteral(c.Right) {
					v.warnAt(pos,
						fmt.Sprintf("numeric operator %q used with string value", c.Op),
						"use == or != for string comparisons")
				}
			}
		})
	}
}

// ─── Cycle detection ──────────────────────────────────────────────────────────

func (v *validator) checkCycles() {
	type dfsState int
	const (
		unvisited dfsState = iota
		inStack
		done
	)
	states := make(map[string]dfsState, len(v.defines))

	var dfs func(name string, path []string)
	dfs = func(name string, path []string) {
		if states[name] == inStack {
			for i, p := range path {
				if p == name {
					chain := append(append([]string{}, path[i:]...), name)
					def := v.defines[name]
					v.errAt(def.Pos,
						fmt.Sprintf("circular dependency: %s", strings.Join(chain, " → ")), "")
					return
				}
			}
		}
		if states[name] != unvisited {
			return
		}
		states[name] = inStack
		if def, ok := v.defines[name]; ok {
			for _, cond := range def.Conditions {
				for _, ref := range collectIsRefs(cond) {
					dfs(ref, append(path, name))
				}
			}
		}
		states[name] = done
	}

	for name := range v.defines {
		if states[name] == unvisited {
			dfs(name, nil)
		}
	}
}

// ─── Workflow validation ──────────────────────────────────────────────────────

func (v *validator) checkWorkflows() {
	for _, b := range v.prog.Blocks {
		wf, ok := b.(*ast.WorkflowBlock)
		if !ok {
			continue
		}

		stepNames := map[string]bool{}
		for _, step := range wf.Steps {
			// Each step must have an MCP call.
			if step.MCPCall == nil {
				v.errAt(wf.Pos, fmt.Sprintf("step %q in workflow %q has no mcp call", step.Name, wf.Name), "")
			}
			// Step names must be unique within the workflow.
			if stepNames[step.Name] {
				v.errAt(wf.Pos, fmt.Sprintf("duplicate step name %q in workflow %q", step.Name, wf.Name), "")
			}
			stepNames[step.Name] = true
		}

		// Validate depends_on references.
		for _, step := range wf.Steps {
			for _, dep := range step.DependsOn {
				if !stepNames[dep] {
					v.errAt(wf.Pos,
						fmt.Sprintf("step %q depends on undefined step %q in workflow %q", step.Name, dep, wf.Name),
						suggest(dep, names(stepNames)))
				}
			}
		}

		// Cycle detection via DFS.
		type dfsState int
		const (
			unvisited dfsState = iota
			inStack
			done
		)
		states := make(map[string]dfsState, len(wf.Steps))
		adj := map[string][]string{}
		for _, step := range wf.Steps {
			for _, dep := range step.DependsOn {
				adj[dep] = append(adj[dep], step.Name)
			}
		}

		var dfs func(name string, path []string)
		dfs = func(name string, path []string) {
			if states[name] == inStack {
				for i, p := range path {
					if p == name {
						chain := append(append([]string{}, path[i:]...), name)
						v.errAt(wf.Pos,
							fmt.Sprintf("circular dependency in workflow %q: %s", wf.Name, strings.Join(chain, " → ")), "")
						return
					}
				}
			}
			if states[name] != unvisited {
				return
			}
			states[name] = inStack
			for _, next := range adj[name] {
				dfs(next, append(path, name))
			}
			states[name] = done
		}

		for _, step := range wf.Steps {
			if states[step.Name] == unvisited {
				dfs(step.Name, nil)
			}
		}
	}
}

// ─── AST walkers ──────────────────────────────────────────────────────────────

func walkBlockConditions(b ast.Block, fn func(ast.Condition)) {
	switch bb := b.(type) {
	case *ast.DetectBlock:
		walkSelector(bb.Selector, fn)
		if bb.Recommend != nil {
			walkCond(bb.Recommend.When, fn)
		}
	case *ast.RuleBlock:
		if bb.Selector != nil {
			walkSelector(*bb.Selector, fn)
		}
		walkCond(bb.When, fn)
	case *ast.RecommendBlock:
		walkCond(bb.When, fn)
		for _, calc := range bb.Calculate {
			for _, c := range calc.Where {
				walkCond(c, fn)
			}
		}
	case *ast.DefineBlock:
		for _, c := range bb.Conditions {
			walkCond(c, fn)
		}
		if bb.ForEach != nil {
			for _, c := range bb.ForEach.Body {
				walkCond(c, fn)
			}
		}
	case *ast.PredictBlock:
		walkSelector(bb.Selector, fn)
		if bb.TrainedOn != nil {
			for _, c := range bb.TrainedOn.Conditions {
				walkCond(c, fn)
			}
		}
	case *ast.ForecastBlock:
		walkSelector(bb.Selector, fn)
		walkCond(bb.When, fn)
	}
}

func walkSelector(sel ast.Selector, fn func(ast.Condition)) {
	for _, c := range sel.Conditions {
		walkCond(c, fn)
	}
}

func walkCond(cond ast.Condition, fn func(ast.Condition)) {
	if cond == nil {
		return
	}
	fn(cond)
	switch c := cond.(type) {
	case *ast.LogicalCondition:
		walkCond(c.Left, fn)
		walkCond(c.Right, fn)
	case *ast.NotCondition:
		walkCond(c.Inner, fn)
	}
}

func collectIsRefs(cond ast.Condition) []string {
	var refs []string
	walkCond(cond, func(c ast.Condition) {
		if ic, ok := c.(*ast.IsCondition); ok && ic.Name != "" {
			refs = append(refs, ic.Name)
		}
	})
	return refs
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

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
	}
	return ast.Pos{}
}

func isStringLiteral(e ast.Expr) bool {
	lit, ok := e.(*ast.LiteralExpr)
	if !ok {
		return false
	}
	_, isStr := lit.Value.(string)
	return isStr
}

func names[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func allNames(m map[string]ast.Block) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (v *validator) errAt(pos ast.Pos, msg, hint string) {
	v.diags.AddError(v.file, pos.Line, pos.Col, msg, hint)
}

func (v *validator) warnAt(pos ast.Pos, msg, hint string) {
	v.diags.AddWarning(v.file, pos.Line, pos.Col, msg, hint)
}

// suggest returns "did you mean X?" when Levenshtein distance ≤ 2.
func suggest(name string, candidates []string) string {
	best, bestDist := "", 999
	for _, c := range candidates {
		if d := levenshtein(name, c); d < bestDist {
			best, bestDist = c, d
		}
	}
	if bestDist <= 2 && best != "" {
		return fmt.Sprintf("did you mean %q?", best)
	}
	return ""
}

func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	row := make([]int, lb+1)
	for j := range row {
		row[j] = j
	}
	for i := 1; i <= la; i++ {
		prev := row[0]
		row[0] = i
		for j := 1; j <= lb; j++ {
			old := row[j]
			if a[i-1] == b[j-1] {
				row[j] = prev
			} else {
				row[j] = 1 + min3(prev, row[j], row[j-1])
			}
			prev = old
		}
	}
	return row[lb]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

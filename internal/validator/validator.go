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
	v.checkOverrides()
	v.checkTemplates()
	v.checkTypes()
	v.checkCycles()
	v.checkWorkflows()
	return v.diags
}

// checkTemplates flags unknown function names inside `{...}` template
// interpolations. The renderer leaves unknowns as their literal source
// so users see what went wrong; this surface gives compile-time
// diagnostics for the same gap before the rule ever fires.
func (v *validator) checkTemplates() {
	for _, b := range v.prog.Blocks {
		pos := blockPos(b)
		for _, tmpl := range collectTemplates(b) {
			if tmpl == nil {
				continue
			}
			for _, n := range tmpl.Nodes {
				fn, ok := n.(*ast.FuncNode)
				if !ok {
					continue
				}
				if _, known := ast.KnownTemplateFunctions[fn.Fn]; !known {
					v.errAt(pos, fmt.Sprintf("template uses unknown function %q", fn.Fn),
						"supported: count, total, sum, avg, min, max, days_until, days_since")
				}
			}
		}
	}
}

// collectTemplates returns every Template embedded in a block, so the
// validator can walk them uniformly. Returns []*ast.Template (never
// dereferences nil).
func collectTemplates(b ast.Block) []*ast.Template {
	switch bb := b.(type) {
	case *ast.DetectBlock:
		return []*ast.Template{bb.Label}
	case *ast.RecommendBlock:
		return []*ast.Template{bb.Suggest}
	case *ast.RuleBlock:
		return []*ast.Template{bb.Reason}
	case *ast.PredictBlock:
		return []*ast.Template{bb.Label}
	case *ast.ForecastBlock:
		return []*ast.Template{bb.Label}
	case *ast.ClusterBlock:
		return []*ast.Template{bb.Label}
	case *ast.ClassifyBlock:
		return []*ast.Template{bb.Label}
	case *ast.SimilarBlock:
		return []*ast.Template{bb.Label}
	case *ast.RelatedBlock:
		return []*ast.Template{bb.Label}
	case *ast.CombineBlock:
		return []*ast.Template{bb.Label}
	}
	return nil
}

// checkOverrides verifies `overrides "name"` clauses on rules: the named rule
// must exist, and you cannot override a strict rule.
func (v *validator) checkOverrides() {
	rules := map[string]*ast.RuleBlock{}
	for _, b := range v.prog.Blocks {
		if r, ok := b.(*ast.RuleBlock); ok {
			rules[r.Name] = r
		}
	}
	ruleNames := make([]string, 0, len(rules))
	for n := range rules {
		ruleNames = append(ruleNames, n)
	}
	for _, b := range v.prog.Blocks {
		r, ok := b.(*ast.RuleBlock)
		if !ok {
			continue
		}
		for _, target := range r.Overrides {
			t, exists := rules[target]
			if !exists {
				v.errAt(r.Pos,
					fmt.Sprintf("rule %q overrides unknown rule %q", r.Name, target),
					suggest(target, ruleNames))
				continue
			}
			if t.Strict {
				v.errAt(r.Pos,
					fmt.Sprintf("rule %q cannot override strict rule %q", r.Name, target),
					"strict rules cannot be defeated")
			}
		}
	}
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
			v.checkDetectTune(bb)
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
		case *ast.CombineBlock:
			v.checkCombine(bb)
		case *ast.RelatedBlock:
			if bb.To == nil && len(bb.Seeds) == 0 {
				v.errAt(bb.Pos, fmt.Sprintf("find related %q requires a 'to' or 'seeds [...]' clause", bb.Name), "")
			}
			if bb.TopK != nil && *bb.TopK <= 0 {
				v.errAt(bb.Pos, fmt.Sprintf("find related %q: top_k must be > 0", bb.Name), "")
			}
			if bb.Damping != nil && (*bb.Damping < 0 || *bb.Damping >= 1) {
				v.errAt(bb.Pos, fmt.Sprintf("find related %q: damping must be in [0, 1)", bb.Name), "")
			}
			if bb.Tol != nil && *bb.Tol <= 0 {
				v.errAt(bb.Pos, fmt.Sprintf("find related %q: tolerance must be > 0", bb.Name), "")
			}
			if bb.MaxIter != nil && *bb.MaxIter <= 0 {
				v.errAt(bb.Pos, fmt.Sprintf("find related %q: max_iterations must be > 0", bb.Name), "")
			}
		}
	}
}

// checkCombine validates combine-block specific constraints.
//
//   - At least one optimize clause.
//   - No duplicate (direction, objective-key) pairs.
//   - v1 ranking mode (no `select`): objectives must be `attr "name"`.
//   - v2 subset mode (with `select K`): objectives may also be aggregates
//     (`total(attr "x")`, `count(records)`, `avg(attr "y")`). Subject_to
//     constraints must have an aggregate LHS and a numeric literal RHS.
//   - `select K` requires K >= 1.
func (v *validator) checkCombine(bb *ast.CombineBlock) {
	// ACO sequence mode validates separately — no minimize/maximize required;
	// the implicit objective is total euclidean distance along the sequence.
	if bb.Sequence {
		v.checkCombineSequence(bb)
		return
	}
	if len(bb.Optimize) == 0 {
		v.errAt(bb.Pos, fmt.Sprintf("combine %q requires at least one 'minimize' or 'maximize' clause", bb.Name), "")
		return
	}
	subsetMode := bb.Select != nil
	if subsetMode && bb.Select.Size < 1 {
		v.errAt(bb.Pos, fmt.Sprintf("combine %q: select size must be >= 1, got %d", bb.Name, bb.Select.Size), "")
	}

	// ILP solver requires single-objective subset selection with linear
	// aggregate constraints. Multi-objective + ILP is intentionally rejected
	// for v2.1 — exact multi-objective Pareto is exponential and the user
	// should explicitly choose GA in that case.
	if bb.Solver == "linear" {
		if !subsetMode {
			v.errAt(bb.Pos, fmt.Sprintf("combine %q: solver linear requires `select K from records`", bb.Name), "")
		}
		if len(bb.Optimize) > 1 {
			v.errAt(bb.Pos, fmt.Sprintf("combine %q: solver linear supports a single objective (got %d) — remove the extra clause or drop `solver linear` to use the GA backend", bb.Name, len(bb.Optimize)), "")
		}
	}

	seen := map[string]bool{}
	for _, oc := range bb.Optimize {
		key, ok := combineObjectiveKey(oc.Attr, subsetMode)
		if !ok {
			if subsetMode {
				v.errAt(bb.Pos, fmt.Sprintf("combine %q: objective must be `attr \"name\"` or `total/count/avg(...)`", bb.Name), "")
			} else {
				v.errAt(bb.Pos, fmt.Sprintf("combine %q: objective must be `attr \"name\"` (use `select K from records` to enable aggregates)", bb.Name), "")
			}
			continue
		}
		fullKey := oc.Direction + ":" + key
		if seen[fullKey] {
			v.errAt(bb.Pos, fmt.Sprintf("combine %q: duplicate objective %s %s", bb.Name, oc.Direction, key), "")
		}
		seen[fullKey] = true
	}

	if !subsetMode && len(bb.Constraints) > 0 {
		v.errAt(bb.Pos, fmt.Sprintf("combine %q: `subject_to` requires `select K from records`", bb.Name), "")
	}
	for _, c := range bb.Constraints {
		if _, ok := c.Left.(*ast.AggregateExpr); !ok {
			v.errAt(c.Pos, fmt.Sprintf("combine %q: subject_to LHS must be an aggregate (total/count/avg)", bb.Name), "")
		}
		if lit, ok := c.Right.(*ast.LiteralExpr); !ok {
			v.errAt(c.Pos, fmt.Sprintf("combine %q: subject_to RHS must be a numeric literal", bb.Name), "")
		} else if _, ok := lit.Value.(float64); !ok {
			v.errAt(c.Pos, fmt.Sprintf("combine %q: subject_to RHS must be numeric, got %T", bb.Name, lit.Value), "")
		}
	}
}

// checkDetectTune validates `tune against test "..."` clauses on detect blocks.
// The referenced test must exist; the block must contain a tunable ML
// primitive (today: anomaly only — extensible).
func (v *validator) checkDetectTune(bb *ast.DetectBlock) {
	if bb.Tune == nil {
		return
	}
	if bb.Tune.AgainstTest == "" {
		v.errAt(bb.Pos, fmt.Sprintf("detect %q: tune clause requires a test name", bb.Name), "")
		return
	}
	// Only enforce existence when the program actually contains test blocks
	// (i.e. we were given a .talon.test file). During `talon build` the rule
	// file is validated in isolation, so the labeled fixture isn't visible
	// yet; testrunner picks up the same check at run time.
	if v.programHasTests() && !v.testExists(bb.Tune.AgainstTest) {
		v.errAt(bb.Pos, fmt.Sprintf("detect %q: tune references unknown test %q", bb.Name, bb.Tune.AgainstTest), "")
	}
	if !detectHasTunablePrimitive(bb) {
		v.errAt(bb.Pos, fmt.Sprintf("detect %q: tune clause requires a tunable ML primitive (today: anomaly)", bb.Name), "")
	}
}

// testExists reports whether any TestBlock in the program (typically from
// a .talon.test file merged in by the CLI) matches the given name.
func (v *validator) testExists(name string) bool {
	for _, b := range v.prog.Blocks {
		if tb, ok := b.(*ast.TestBlock); ok && tb.Name == name {
			return true
		}
	}
	return false
}

// programHasTests reports whether the program contains any TestBlock.
// Used to gate validator checks that depend on labeled fixtures being
// visible (which is only true once `talon test` merges the .talon.test).
func (v *validator) programHasTests() bool {
	for _, b := range v.prog.Blocks {
		if _, ok := b.(*ast.TestBlock); ok {
			return true
		}
	}
	return false
}

// detectHasTunablePrimitive reports whether a detect block contains a
// primitive whose parameters ABC tuning currently supports. v1 tunes
// `anomaly_zscore` (threshold) and `learned_threshold` (percentile).
func detectHasTunablePrimitive(bb *ast.DetectBlock) bool {
	if bb.Anomaly != nil {
		return true
	}
	for _, cond := range bb.Selector.Conditions {
		if hasTunableCondition(cond) {
			return true
		}
	}
	return false
}

// hasTunableCondition walks the selector tree looking for primitive-tunable
// shapes: anomaly compared_to OR learned_threshold comparisons.
func hasTunableCondition(cond ast.Condition) bool {
	switch c := cond.(type) {
	case *ast.AnomalyCondition:
		return true
	case *ast.CompareCondition:
		if _, ok := c.Right.(*ast.LearnedThresholdExpr); ok {
			return true
		}
	case *ast.LogicalCondition:
		return hasTunableCondition(c.Left) || hasTunableCondition(c.Right)
	case *ast.NotCondition:
		return hasTunableCondition(c.Inner)
	}
	return false
}

// checkCombineSequence validates the ACO sequence mode: requires a
// `coordinates` clause naming two attr exprs; rejects `select`, `subject_to`,
// `minimize`/`maximize`, and `solver linear` since none apply to routing.
func (v *validator) checkCombineSequence(bb *ast.CombineBlock) {
	if bb.Coordinates == nil {
		v.errAt(bb.Pos, fmt.Sprintf("combine %q: sequence mode requires `coordinates attr \"X\", attr \"Y\"`", bb.Name), "")
		return
	}
	if _, ok := bb.Coordinates.X.(*ast.AttrExpr); !ok {
		v.errAt(bb.Pos, fmt.Sprintf("combine %q: coordinates X must be `attr \"name\"`", bb.Name), "")
	}
	if _, ok := bb.Coordinates.Y.(*ast.AttrExpr); !ok {
		v.errAt(bb.Pos, fmt.Sprintf("combine %q: coordinates Y must be `attr \"name\"`", bb.Name), "")
	}
	if len(bb.Optimize) > 0 {
		v.errAt(bb.Pos, fmt.Sprintf("combine %q: sequence mode optimizes total distance implicitly — remove `minimize`/`maximize` (or drop `sequence` to use GA)", bb.Name), "")
	}
	if bb.Select != nil {
		v.errAt(bb.Pos, fmt.Sprintf("combine %q: sequence mode visits every candidate — `select K` is not applicable", bb.Name), "")
	}
	if len(bb.Constraints) > 0 {
		v.errAt(bb.Pos, fmt.Sprintf("combine %q: sequence mode does not yet support `subject_to`", bb.Name), "")
	}
	if bb.Solver != "" {
		v.errAt(bb.Pos, fmt.Sprintf("combine %q: sequence mode uses ACO; `solver` is not applicable", bb.Name), "")
	}
}

// combineObjectiveKey produces a stable identifier for an objective expression
// for duplicate detection, and reports whether the shape is allowed.
func combineObjectiveKey(e ast.Expr, allowAggregate bool) (string, bool) {
	switch x := e.(type) {
	case *ast.AttrExpr:
		return "attr:" + x.Name, true
	case *ast.AggregateExpr:
		if !allowAggregate {
			return "", false
		}
		if attr, ok := x.Arg.(*ast.AttrExpr); ok {
			return x.Fn + ":attr:" + attr.Name, true
		}
		if x.Arg == nil && x.Fn == "count" {
			return "count:records", true
		}
		return "", false
	}
	return "", false
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
	case *ast.RelatedBlock:
		walkSelector(bb.Selector, fn)
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
	case *ast.RelatedBlock:
		return bb.Pos
	case *ast.OnBlock:
		return bb.Pos
	case *ast.ConstraintBlock:
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

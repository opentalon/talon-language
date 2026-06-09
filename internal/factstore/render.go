package factstore

import (
	"fmt"
	"strings"
)

// String renders the query in Datalog (Datomic/Datalevin) syntax. The
// representation is canonical: any backend that already speaks Datalog
// can use it as a wire format, and any backend that doesn't can use it
// as a debug/trace string. The testrunner emits it in traces; the
// Datalevin client sends it on the wire; the REPL prints it under
// `:trace`.
//
// When Aggregates is non-empty, the :find clause holds the GroupBy
// columns followed by one aggregate expression per Aggregate. Find is
// ignored in that case — the planner shouldn't set both.
func (q Query) String() string {
	queryText, _ := q.renderInto()
	return queryText
}

// QueryArgs returns the extra positional arguments the renderer
// inlined as `:in` placeholders — currently only FullText.Expr values
// (Datalevin's structured search syntax can't ride inside a `:where`
// clause; it has to be passed via `:in`). Order matches the
// declaration order in the rendered query.
func (q Query) QueryArgs() []string {
	_, args := q.renderInto()
	return args
}

// renderInto does the actual work: walks the query once, emitting
// Datalog text and collecting `:in` arg values in scan order. We do
// both in one pass so the rendered placeholders (`?fts-q-0`, ...)
// stay in sync with the returned arg list.
func (q Query) renderInto() (string, []string) {
	var args []string
	exprIndex := func() string {
		name := fmt.Sprintf("?fts-q-%d", len(args))
		args = append(args, "") // placeholder; filled after we know the Expr
		return name
	}
	pushExpr := func(expr string) string {
		name := exprIndex()
		args[len(args)-1] = expr
		return name
	}

	var b strings.Builder
	b.WriteString("[:find ")
	switch {
	case len(q.Pull) > 0:
		for i, p := range q.Pull {
			if i > 0 {
				b.WriteString(" ")
			}
			b.WriteString(fmt.Sprintf("(pull %s %s)", p.EntityVar, p.Pattern))
		}
	case len(q.Aggregates) > 0:
		first := true
		for _, v := range q.GroupBy {
			if !first {
				b.WriteString(" ")
			}
			b.WriteString(v)
			first = false
		}
		for _, a := range q.Aggregates {
			if !first {
				b.WriteString(" ")
			}
			b.WriteString(renderAggregate(a))
			first = false
		}
	default:
		b.WriteString(strings.Join(q.Find, " "))
	}

	// Pre-scan Where for FullText.Expr to know how many `?fts-q-N`
	// placeholders the :in clause needs. We rebuild the Where rendering
	// in the same scan order so indices line up.
	var inVars []string
	for _, c := range q.Where {
		collectFullTextExprVars(c, pushExpr, &inVars)
	}
	if len(q.Rules) > 0 || len(inVars) > 0 {
		b.WriteString("\n :in $")
		if len(q.Rules) > 0 {
			b.WriteString(" %")
		}
		for _, v := range inVars {
			b.WriteString(" ")
			b.WriteString(v)
		}
	}

	// Now render Where using the same iteration order so each
	// FullText.Expr clause picks up the matching `?fts-q-N`.
	idx := 0
	b.WriteString("\n :where")
	for _, c := range q.Where {
		b.WriteString("\n ")
		b.WriteString(renderClauseWithArgs(c, &idx))
	}
	b.WriteString("]")
	return b.String(), args
}

// collectFullTextExprVars walks one clause subtree, allocating a
// `?fts-q-N` variable for each FullText with a non-empty Expr field.
// Order matters: the same scan order is used by renderClauseWithArgs
// so each clause consumes the matching arg.
func collectFullTextExprVars(c Clause, push func(string) string, out *[]string) {
	switch cc := c.(type) {
	case *FullText:
		if cc.Expr != "" {
			*out = append(*out, push(cc.Expr))
		}
	case *Or:
		for _, branch := range cc.Branches {
			for _, sub := range branch {
				collectFullTextExprVars(sub, push, out)
			}
		}
	case *Not:
		for _, sub := range cc.Body {
			collectFullTextExprVars(sub, push, out)
		}
	}
}

// RulesString renders the Rules vector in Datalevin's wire form. Each
// rule body is a clause list; rules sharing the same Name form a
// disjunction. Returns "" when Rules is empty so the Datalevin client
// can skip the field on POST.
//
//	[[(category-in-tree ?c ?root)
//	  [(= ?c ?root)]]
//	 [(category-in-tree ?c ?root)
//	  [?cent :record/type "category"]
//	  [?cent :category/name ?c]
//	  [?cent :category/parent ?p]
//	  (category-in-tree ?p ?root)]]
func (q Query) RulesString() string {
	if len(q.Rules) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[")
	for i, r := range q.Rules {
		if i > 0 {
			b.WriteString("\n ")
		}
		b.WriteString("[(")
		b.WriteString(r.Name)
		for _, a := range r.Args {
			b.WriteString(" ")
			b.WriteString(a)
		}
		b.WriteString(")")
		for _, c := range r.Body {
			b.WriteString("\n  ")
			b.WriteString(renderClause(c))
		}
		b.WriteString("]")
	}
	b.WriteString("]")
	return b.String()
}

// renderAggregate emits the Datalog form for one aggregate: `(count ?e)`,
// `(avg ?x)`, `(sum ?x)`, `(min ?x)`, `(max ?x)`. Datalevin accepts
// `(total ?x)` as a synonym for `(sum ?x)`; we normalise to `sum` so the
// wire format is uniform.
func renderAggregate(a Aggregate) string {
	fn := a.Fn
	if fn == "total" {
		fn = "sum"
	}
	if fn == "count" && (a.Over.IsWildcard() || a.Over.Var == "") {
		// `(count ?e)` is the conventional row-count form when there's
		// no obvious column to count. Fall back to the entity variable.
		return "(count ?e)"
	}
	return fmt.Sprintf("(%s %s)", fn, renderTerm(a.Over))
}

func renderClause(c Clause) string {
	idx := 0
	return renderClauseWithArgs(c, &idx)
}

// renderClauseWithArgs is the main clause renderer. `idx` is the
// running counter of FullText.Expr clauses already seen — each Expr
// clause consumes one `?fts-q-N` placeholder and increments. The
// counter must reflect the same scan order that
// collectFullTextExprVars used in renderInto so the :in placeholders
// match positionally.
func renderClauseWithArgs(c Clause, idx *int) string {
	switch cc := c.(type) {
	case *Pattern:
		return renderPattern(cc)
	case *Predicate:
		return renderPredicate(cc)
	case *Or:
		return renderOrWithArgs(cc, idx)
	case *Not:
		return renderNotWithArgs(cc, idx)
	case *FullText:
		return renderFullTextWithArgs(cc, idx)
	case *RuleCall:
		return renderRuleCall(cc)
	}
	return ""
}

// renderFullText emits Datalevin's full-text predicate. Three shapes:
//
//	[(fulltext $ "query")        [[?e ?a ?v]]]   ; whole-db search
//	[(fulltext $ :attr "query")  [[?e ?a ?v]]]   ; single-attribute scope
//	[(fulltext $ [:and {:phrase "lamb"} "red"]) [[?e ?a ?v]]]
//	                                             ; raw expr form (Expr)
//
// The destructured binding pulls the matched entity into ?e (or the
// caller-supplied entity var) and ignores ?a/?v. Per Datalevin's
// default `:display :refs` mode fulltext returns `[e a v]` tuples;
// asking for four (with a score) requires `:display :texts+offsets`
// in the options map, which we don't surface here.
//
// Datalevin requires attributes to be configured with `:db/fulltext
// true` for this to use the FTS index. MemoryStore's matchFullText
// fallback handles the simple Query field via substring scan.
//
// renderFullTextWithArgs picks the wire shape based on what's set:
//
//	[(fulltext $ "query")              [[?e ?a ?v]]]   ; whole-db, literal
//	[(fulltext $ :attr "query")        [[?e ?a ?v]]]   ; attr-scoped, literal
//	[(fulltext $ ?fts-q-N)             [[?e ?a ?v]]]   ; Expr, passed via :in
//	[(fulltext $ :attr ?fts-q-N)       [[?e ?a ?v]]]   ; Expr + attr scope
//
// Datalevin's structured search expressions (`[:and {:phrase "..."}
// "X"]`) can't be embedded inside a `:where` clause — they have to
// ride on a `:in` parameter. The Expr branch references the
// placeholder by its 1-based scan index so each clause picks up the
// matching pre-allocated arg.
func renderFullTextWithArgs(f *FullText, idx *int) string {
	eVar := f.Entity.Var
	if eVar == "" {
		eVar = "?e"
	}
	var qArg string
	if f.Expr != "" {
		qArg = fmt.Sprintf("?fts-q-%d", *idx)
		*idx++
	} else {
		qArg = fmt.Sprintf("%q", f.Query)
	}
	if f.Attribute != "" {
		return fmt.Sprintf("[(fulltext $ %s %s) [[%s ?ft-a ?ft-v]]]", f.Attribute, qArg, eVar)
	}
	return fmt.Sprintf("[(fulltext $ %s) [[%s ?ft-a ?ft-v]]]", qArg, eVar)
}

func renderRuleCall(r *RuleCall) string {
	parts := make([]string, 0, len(r.Args))
	for _, a := range r.Args {
		parts = append(parts, renderTerm(a))
	}
	return fmt.Sprintf("(%s %s)", r.Name, strings.Join(parts, " "))
}

func renderPattern(p *Pattern) string {
	return fmt.Sprintf("[%s %s %s]", renderTerm(p.Entity), p.Attribute, renderTerm(p.Value))
}

func renderPredicate(p *Predicate) string {
	switch p.Op {
	case "in", "not_in":
		set := renderSet(p.Right)
		body := fmt.Sprintf("(contains? %s %s)", set, renderTerm(p.Left))
		if p.Op == "not_in" {
			return "[(not " + body + ")]"
		}
		return "[" + body + "]"
	case "starts_with":
		return fmt.Sprintf("[(clojure.string/starts-with? %s %s)]", renderTerm(p.Left), renderTerm(p.Right))
	case "ends_with":
		return fmt.Sprintf("[(clojure.string/ends-with? %s %s)]", renderTerm(p.Left), renderTerm(p.Right))
	case "contains":
		return fmt.Sprintf("[(clojure.string/includes? %s %s)]", renderTerm(p.Left), renderTerm(p.Right))
	}
	return fmt.Sprintf("[(%s %s %s)]", datalogOp(p.Op), renderTerm(p.Left), renderTerm(p.Right))
}

func renderOrWithArgs(o *Or, idx *int) string {
	parts := make([]string, 0, len(o.Branches))
	for _, branch := range o.Branches {
		clauses := make([]string, 0, len(branch))
		for _, c := range branch {
			clauses = append(clauses, renderClauseWithArgs(c, idx))
		}
		parts = append(parts, strings.Join(clauses, " "))
	}
	return "(or\n     " + strings.Join(parts, "\n     ") + ")"
}

func renderNotWithArgs(n *Not, idx *int) string {
	clauses := make([]string, 0, len(n.Body))
	for _, c := range n.Body {
		clauses = append(clauses, renderClauseWithArgs(c, idx))
	}
	return "(not " + strings.Join(clauses, " ") + ")"
}

func renderTerm(t Term) string {
	if t.IsVar() {
		return t.Var
	}
	return renderLiteral(t.Literal)
}

func renderLiteral(v any) string {
	switch n := v.(type) {
	case nil:
		return ""
	case string:
		return fmt.Sprintf("%q", n)
	case bool:
		if n {
			return "true"
		}
		return "false"
	case float64:
		if n == float64(int64(n)) {
			return fmt.Sprintf("%d", int64(n))
		}
		return fmt.Sprintf("%g", n)
	case int:
		return fmt.Sprintf("%d", n)
	case int64:
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%v", v)
}

func renderSet(t Term) string {
	members, ok := t.Literal.([]any)
	if !ok {
		return "#{}"
	}
	rendered := make([]string, 0, len(members))
	for _, m := range members {
		rendered = append(rendered, renderLiteral(m))
	}
	return "#{" + strings.Join(rendered, " ") + "}"
}

func datalogOp(op string) string {
	switch op {
	case "==":
		return "="
	case "!=":
		return "not="
	}
	return op
}

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
	var b strings.Builder
	b.WriteString("[:find ")
	if len(q.Aggregates) > 0 {
		// Group-by columns first, then aggregate expressions.
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
	} else {
		b.WriteString(strings.Join(q.Find, " "))
	}
	b.WriteString("\n :where")
	for _, c := range q.Where {
		b.WriteString("\n ")
		b.WriteString(renderClause(c))
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
	switch cc := c.(type) {
	case *Pattern:
		return renderPattern(cc)
	case *Predicate:
		return renderPredicate(cc)
	case *Or:
		return renderOr(cc)
	case *Not:
		return renderNot(cc)
	case *FullText:
		return renderFullText(cc)
	}
	return ""
}

// renderFullText emits Datalevin's full-text predicate:
//
//	[(fulltext $ "query") [[?e ?a ?v ?s]]]
//
// The destructured binding pulls the matched entity into ?e (or the
// caller-supplied entity var) and ignores ?a/?v/?s — we only care that
// the entity is anchored so sibling Pattern clauses join correctly.
//
// Datalevin requires attributes to be configured with `:db/fulltext
// true` for this to use the FTS index; otherwise the server falls back
// to a sequential scan, matching MemoryStore's behaviour.
func renderFullText(f *FullText) string {
	eVar := f.Entity.Var
	if eVar == "" {
		eVar = "?e"
	}
	return fmt.Sprintf("[(fulltext $ %q) [[%s ?ft-a ?ft-v ?ft-s]]]", f.Query, eVar)
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

func renderOr(o *Or) string {
	parts := make([]string, 0, len(o.Branches))
	for _, branch := range o.Branches {
		clauses := make([]string, 0, len(branch))
		for _, c := range branch {
			clauses = append(clauses, renderClause(c))
		}
		parts = append(parts, strings.Join(clauses, " "))
	}
	return "(or\n     " + strings.Join(parts, "\n     ") + ")"
}

func renderNot(n *Not) string {
	clauses := make([]string, 0, len(n.Body))
	for _, c := range n.Body {
		clauses = append(clauses, renderClause(c))
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

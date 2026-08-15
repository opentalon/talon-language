package factstore

import "fmt"

// ruleCtx carries the resolution state shared across all RuleCalls in
// one Query: the rule index, a per-call memo so repeated calls share
// work, and an in-progress set for cycle short-circuiting. Resolution
// is top-down with tabling, so a `(category-in-tree ?c "Tools")` call
// substitutes "Tools" into the body before evaluating, which is what
// lets the trivial `(= ?c ?root)` base case bind ?c="Tools" instead
// of dying for lack of an enumeration source.
type ruleCtx struct {
	store   *MemoryStore
	byName  map[string][]Rule
	memo    map[string][][]any
	visited map[string]bool

	// hasNegation routes resolution through the well-founded evaluator when any
	// rule body carries a Negation clause; wf caches the computed model.
	hasNegation bool
	wf          *wfModel
}

func newRuleCtx(m *MemoryStore, rules []Rule) *ruleCtx {
	byName := map[string][]Rule{}
	hasNeg := false
	for _, r := range rules {
		byName[r.Name] = append(byName[r.Name], r)
		if bodyHasNegation(r.Body) {
			hasNeg = true
		}
	}
	return &ruleCtx{
		store:       m,
		byName:      byName,
		memo:        map[string][][]any{},
		visited:     map[string]bool{},
		hasNegation: hasNeg,
	}
}

// resolve returns every tuple matching `name(args...)` where nil
// entries in args are free and non-nil entries are caller-supplied
// bindings substituted into each rule body before evaluation.
//
// Memoization keys on the binding shape so cycles like a category
// chain that loops back return empty instead of recursing forever.
// For acyclic category trees the memo is a perf cache.
func (rc *ruleCtx) resolve(name string, args []any) [][]any {
	if rc.hasNegation {
		// Negation-bearing rule sets have no order-independent top-down reading;
		// answer from the well-founded model instead.
		return rc.wfResolve(name, args)
	}
	key := callKey(name, args)
	if cached, ok := rc.memo[key]; ok {
		return cached
	}
	if rc.visited[key] {
		return nil // cycle
	}
	rc.visited[key] = true
	defer func() { rc.visited[key] = false }()

	var out [][]any
	for _, rule := range rc.byName[name] {
		seed := map[string]any{}
		for i, a := range rule.Args {
			if args[i] != nil {
				seed[a] = args[i]
			}
		}
		rc.enumerate(rule.Body, 0, seed, func(b map[string]any) {
			tuple := make([]any, len(rule.Args))
			for i, a := range rule.Args {
				tuple[i] = b[a]
			}
			out = appendUniqueTuple(out, tuple)
		})
	}
	rc.memo[key] = out
	return out
}

// enumerate walks a rule body left-to-right. Pattern clauses iterate
// the store; RuleCall clauses recurse via rc.resolve; "=" predicates
// unify or check.
func (rc *ruleCtx) enumerate(body []Clause, i int, bindings map[string]any, yield func(map[string]any)) {
	if i == len(body) {
		yield(cloneBindings(bindings))
		return
	}
	switch c := body[i].(type) {
	case *Pattern:
		for id, attrs := range rc.store.entities {
			val, ok := attrs[c.Attribute]
			if !ok {
				continue
			}
			next := cloneBindings(bindings)
			if !unifyTerm(c.Entity, float64(id), next) {
				continue
			}
			if !unifyTerm(c.Value, val, next) {
				continue
			}
			rc.enumerate(body, i+1, next, yield)
		}
	case *RuleCall:
		callArgs := make([]any, len(c.Args))
		for j, a := range c.Args {
			switch {
			case a.IsVar():
				if v, ok := bindings[a.Var]; ok {
					callArgs[j] = v
				}
			case a.IsWildcard():
				// free
			default:
				callArgs[j] = a.Literal
			}
		}
		tuples := rc.resolve(c.Name, callArgs)
		for _, t := range tuples {
			next := cloneBindings(bindings)
			ok := true
			for j, a := range c.Args {
				if !unifyTerm(a, t[j], next) {
					ok = false
					break
				}
			}
			if ok {
				rc.enumerate(body, i+1, next, yield)
			}
		}
	case *Predicate:
		switch c.Op {
		case "=", "==":
			// Unify-or-check: equality may *bind* an unbound side, so it can
			// introduce a value the rest of the body carries forward.
			l := resolveTerm(c.Left, bindings)
			r := resolveTerm(c.Right, bindings)
			switch {
			case l != nil && r != nil:
				if equalValues(l, r) {
					rc.enumerate(body, i+1, bindings, yield)
				}
			case c.Left.IsVar() && r != nil:
				next := cloneBindings(bindings)
				next[c.Left.Var] = r
				rc.enumerate(body, i+1, next, yield)
			case c.Right.IsVar() && l != nil:
				next := cloneBindings(bindings)
				next[c.Right.Var] = l
				rc.enumerate(body, i+1, next, yield)
			}
		default:
			// Every other predicate is a GUARD on already-bound values —
			// comparisons (< <= > >= !=), string tests, and membership. A guard
			// only filters existing bindings; it never binds a fresh variable,
			// so it cannot invent values outside the EDB and the semi-naive
			// fixpoint still terminates. matchPredicate returns false when an
			// operand is unbound (the range-restriction / safety condition),
			// which prunes the branch. See ADR 0010.
			if matchPredicate(c, bindings) {
				rc.enumerate(body, i+1, bindings, yield)
			}
		}
	}
}

func unifyTerm(t Term, val any, bindings map[string]any) bool {
	if t.IsWildcard() {
		return true
	}
	if t.IsVar() {
		if existing, bound := bindings[t.Var]; bound {
			return equalValues(existing, val)
		}
		bindings[t.Var] = val
		return true
	}
	return equalValues(t.Literal, val)
}

func appendUniqueTuple(s [][]any, t []any) [][]any {
	for _, existing := range s {
		if tuplesEqual(existing, t) {
			return s
		}
	}
	return append(s, t)
}

func tuplesEqual(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalValues(a[i], b[i]) {
			return false
		}
	}
	return true
}

func callKey(name string, args []any) string {
	key := name
	for _, a := range args {
		if a == nil {
			key += "|_"
		} else {
			key += fmt.Sprintf("|%v", a)
		}
	}
	return key
}

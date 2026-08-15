package factstore

import "fmt"

// Negation is negation-as-failure of a rule-call inside a Rule body. Unlike the
// query-level Not (a set difference over one query's rows), a Negation that
// appears in a recursive rule is resolved under *well-founded semantics*: the
// rule set is grounded against the store and evaluated to a unique three-valued
// model — true / false / undefined — via an alternating fixpoint. That is what
// lets negation appear even through recursion, e.g.
//
//	win(X) :- move(X, Y), not win(Y)
//
// without the answer depending on evaluation order. Positions in an even
// negative loop (a draw) come out *undefined* rather than arbitrarily true or
// false.
//
// Safety: every variable in a rule's head, positive rule-calls, and negated
// rule-calls must be bound by a Pattern or an `=` predicate in the same body
// (range restriction) — the same anchoring the planner already emits. A rule
// set that mixes negation with generative positive recursion (a positive
// rule-call that binds a fresh head variable) is out of scope; keep positive
// recursion in negation-free rule sets, which take the top-down resolver.
type Negation struct {
	Name string
	Args []Term
}

func (*Negation) clauseNode() {}

// Three-valued truth of a ground atom. Absence from wfModel.truth means false.
const (
	wfTrue      = 1
	wfUndefined = 2
)

// groundRule is one variable-free instance of a Rule: a head atom that holds
// when every positive dependency holds and no negative dependency does. Atoms
// are canonical string keys (see atomKey).
type groundRule struct {
	head string
	pos  []string
	neg  []string
}

// wfModel is the computed well-founded model. atoms/names carry the argument
// values and predicate name for every head atom so answers can be
// reconstructed; truth records only true and undefined atoms.
type wfModel struct {
	atoms map[string][]any
	names map[string]string
	truth map[string]int
}

// bodyHasNegation reports whether any top-level body clause is a Negation.
func bodyHasNegation(body []Clause) bool {
	for _, c := range body {
		if _, ok := c.(*Negation); ok {
			return true
		}
	}
	return false
}

// wellFounded grounds every rule and computes the well-founded model via the
// alternating fixpoint. Memoized on the ruleCtx — the model is a property of
// the rule set + store, independent of any single call.
func (rc *ruleCtx) wellFounded() *wfModel {
	if rc.wf != nil {
		return rc.wf
	}
	var grules []groundRule
	atoms := map[string][]any{}
	names := map[string]string{}
	for _, rules := range rc.byName {
		for _, rule := range rules {
			rc.groundRule(rule, &grules, atoms, names)
		}
	}

	// Alternating fixpoint (Van Gelder). A(S) is the least set derivable when
	// each `not a` is assumed true iff a ∉ S; A is antitone, so A² is monotone
	// and its least fixpoint from ∅ is the well-founded true set. A(true) then
	// adds the atoms that are true-or-undefined, so anything outside it is
	// false and the gap between the two is undefined.
	trueSet := map[string]bool{}
	for {
		next := oneStepWF(grules, oneStepWF(grules, trueSet))
		if sameStrSet(next, trueSet) {
			break
		}
		trueSet = next
	}
	nonFalse := oneStepWF(grules, trueSet)

	truth := map[string]int{}
	for k := range atoms {
		switch {
		case trueSet[k]:
			truth[k] = wfTrue
		case nonFalse[k]:
			truth[k] = wfUndefined
		}
	}
	rc.wf = &wfModel{atoms: atoms, names: names, truth: truth}
	return rc.wf
}

// groundRule enumerates the bindings of one rule over its Pattern/`=` literals
// (the binding generators) and, for each, emits a ground instance capturing the
// head plus its positive rule-call and negated dependencies. Instances whose
// head or dependency arguments aren't fully bound are dropped (range
// restriction).
func (rc *ruleCtx) groundRule(rule Rule, out *[]groundRule, atoms map[string][]any, names map[string]string) {
	var gens []Clause
	var pos []*RuleCall
	var neg []*Negation
	for _, c := range rule.Body {
		switch cc := c.(type) {
		case *Pattern:
			gens = append(gens, cc)
		case *Predicate:
			gens = append(gens, cc)
		case *RuleCall:
			pos = append(pos, cc)
		case *Negation:
			neg = append(neg, cc)
		}
	}

	rc.enumGen(gens, 0, map[string]any{}, func(b map[string]any) {
		headArgs, ok := groundVars(rule.Args, b)
		if !ok {
			return
		}
		headKey := atomKey(rule.Name, headArgs)
		if _, seen := atoms[headKey]; !seen {
			atoms[headKey] = headArgs
			names[headKey] = rule.Name
		}

		gr := groundRule{head: headKey}
		for _, p := range pos {
			a, ok := groundTerms(p.Args, b)
			if !ok {
				return
			}
			gr.pos = append(gr.pos, atomKey(p.Name, a))
		}
		for _, n := range neg {
			a, ok := groundTerms(n.Args, b)
			if !ok {
				return
			}
			gr.neg = append(gr.neg, atomKey(n.Name, a))
		}
		*out = append(*out, gr)
	})
}

// enumGen walks the binding-generator clauses (Pattern, `=`) exactly as the
// top-down resolver's enumerate does, yielding one binding map per solution.
func (rc *ruleCtx) enumGen(gens []Clause, i int, b map[string]any, yield func(map[string]any)) {
	if i == len(gens) {
		yield(b)
		return
	}
	switch c := gens[i].(type) {
	case *Pattern:
		for id, attrs := range rc.store.entities {
			val, ok := attrs[c.Attribute]
			if !ok {
				continue
			}
			next := cloneBindings(b)
			if !unifyTerm(c.Entity, float64(id), next) {
				continue
			}
			if !unifyTerm(c.Value, val, next) {
				continue
			}
			rc.enumGen(gens, i+1, next, yield)
		}
	case *Predicate:
		switch c.Op {
		case "=", "==":
			// Unify-or-check: may bind an unbound side.
			l := resolveTerm(c.Left, b)
			r := resolveTerm(c.Right, b)
			switch {
			case l != nil && r != nil:
				if equalValues(l, r) {
					rc.enumGen(gens, i+1, b, yield)
				}
			case c.Left.IsVar() && r != nil:
				next := cloneBindings(b)
				next[c.Left.Var] = r
				rc.enumGen(gens, i+1, next, yield)
			case c.Right.IsVar() && l != nil:
				next := cloneBindings(b)
				next[c.Right.Var] = l
				rc.enumGen(gens, i+1, next, yield)
			}
		default:
			// Guard on already-bound values (comparisons, string, membership):
			// filters, never binds, so it stays within range restriction and the
			// alternating fixpoint still converges. See ADR 0010.
			if matchPredicate(c, b) {
				rc.enumGen(gens, i+1, b, yield)
			}
		}
	}
}

// oneStepWF is A(assumed): the least set of atoms derivable when every negated
// literal `not n` counts as satisfied iff n ∉ assumed.
func oneStepWF(grules []groundRule, assumed map[string]bool) map[string]bool {
	derived := map[string]bool{}
	for {
		changed := false
		for _, r := range grules {
			if derived[r.head] {
				continue
			}
			ok := true
			for _, p := range r.pos {
				if !derived[p] {
					ok = false
					break
				}
			}
			if ok {
				for _, n := range r.neg {
					if assumed[n] {
						ok = false
						break
					}
				}
			}
			if ok {
				derived[r.head] = true
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return derived
}

// wfResolve answers a rule call from the well-founded model: it returns the
// argument tuples of every *true* atom for the predicate whose bound arguments
// match. Undefined atoms are not returned — a query yields definite answers.
func (rc *ruleCtx) wfResolve(name string, args []any) [][]any {
	m := rc.wellFounded()
	var out [][]any
	for key, atomArgs := range m.atoms {
		if m.names[key] != name || m.truth[key] != wfTrue || len(atomArgs) != len(args) {
			continue
		}
		match := true
		for i, a := range args {
			if a != nil && !equalValues(a, atomArgs[i]) {
				match = false
				break
			}
		}
		if match {
			out = appendUniqueTuple(out, append([]any(nil), atomArgs...))
		}
	}
	return out
}

// groundVars resolves a rule head's variable names against a binding map.
func groundVars(vars []string, b map[string]any) ([]any, bool) {
	out := make([]any, len(vars))
	for i, v := range vars {
		val, ok := b[v]
		if !ok {
			return nil, false
		}
		out[i] = val
	}
	return out, true
}

// groundTerms resolves a rule-call's argument terms; a wildcard or unbound
// variable makes the instance non-ground.
func groundTerms(terms []Term, b map[string]any) ([]any, bool) {
	out := make([]any, len(terms))
	for i, t := range terms {
		switch {
		case t.IsWildcard():
			return nil, false
		case t.IsVar():
			val, ok := b[t.Var]
			if !ok {
				return nil, false
			}
			out[i] = val
		default:
			out[i] = t.Literal
		}
	}
	return out, true
}

func atomKey(name string, args []any) string {
	key := name
	for _, a := range args {
		key += fmt.Sprintf("|%v", a)
	}
	return key
}

func sameStrSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

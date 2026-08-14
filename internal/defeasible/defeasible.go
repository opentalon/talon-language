// Package defeasible resolves conflicts between rule blocks that match the
// same target. See docs/defeasible.md and issue #23 for the language model.
//
// The executor evaluates a rule's selector/when conditions to decide which
// rules apply to a given target. When multiple rules apply, this package
// picks which ones should actually fire, using two mechanisms:
//
//  1. Explicit override edges (`overrides "name"`): if rule B overrides rule
//     A and both match, A is defeated.
//  2. Priority levels (LOW < MEDIUM < HIGH < CRITICAL): among the surviving
//     defeasible rules, the highest priority wins.
//
// Strict rules (`strict rule ...`) cannot be defeated. They always fire when
// their conditions match, alongside any defeasible winners.
//
// Unresolved ties (multiple defeasible rules at the same highest priority
// with no override edge between them) are reported as warnings rather than
// silently picking one — the caller surfaces the warning so the author can
// add an `overrides` clause or adjust priority.
package defeasible

import (
	"fmt"

	"github.com/opentalon/tln-language/internal/ast"
)

// Resolve takes the set of rules that match a given target and returns the
// rules whose actions should fire, plus any warnings for unresolved ties.
//
// Input order is not significant; resolution is deterministic given the same
// set of inputs (winners are returned sorted by rule name).
func Resolve(matched []*ast.RuleBlock) (winners []*ast.RuleBlock, warnings []string) {
	if len(matched) == 0 {
		return nil, nil
	}

	byName := make(map[string]*ast.RuleBlock, len(matched))
	for _, r := range matched {
		byName[r.Name] = r
	}

	// 1. Strict rules always survive. They are returned as part of the
	//    winners but are not subject to override/priority resolution.
	var strict, defeasible []*ast.RuleBlock
	for _, r := range matched {
		if r.Strict {
			strict = append(strict, r)
		} else {
			defeasible = append(defeasible, r)
		}
	}

	// 2. Build the defeated set from override edges. We only count an
	//    override if both endpoints are in the matched set — overriding a
	//    rule that didn't fire is a no-op.
	defeated := make(map[string]bool, len(defeasible))
	for _, r := range matched { // strict rules can also override
		for _, target := range r.Overrides {
			if _, ok := byName[target]; ok {
				defeated[target] = true
			}
		}
	}

	var surviving []*ast.RuleBlock
	for _, r := range defeasible {
		if !defeated[r.Name] {
			surviving = append(surviving, r)
		}
	}

	// 3. Pick the highest-priority survivors. Multiple survivors at the same
	//    highest priority with no override between them are an unresolved
	//    tie — emit them all and warn the caller.
	if len(surviving) > 0 {
		top := priorityOf(surviving[0])
		for _, r := range surviving[1:] {
			if priorityOf(r) > top {
				top = priorityOf(r)
			}
		}
		var topRules []*ast.RuleBlock
		for _, r := range surviving {
			if priorityOf(r) == top {
				topRules = append(topRules, r)
			}
		}
		if len(topRules) > 1 {
			warnings = append(warnings, fmt.Sprintf(
				"defeasible: rules %s tie at priority %s — add an `overrides` clause or adjust priorities",
				ruleNameList(topRules), priorityName(top),
			))
		}
		winners = append(winners, topRules...)
	}

	winners = append(winners, strict...)
	sortByName(winners)
	return winners, warnings
}

// priorityOf returns the rule's priority, defaulting to MEDIUM when nil.
// MEDIUM matches the implicit default for shipped MCP rules in the model
// described by issue #23; LOW is reserved for auto-discovered rules that
// must opt in explicitly.
func priorityOf(r *ast.RuleBlock) ast.Priority {
	if r.Priority == nil {
		return ast.PriorityMedium
	}
	return *r.Priority
}

func priorityName(p ast.Priority) string {
	switch p {
	case ast.PriorityLow:
		return "LOW"
	case ast.PriorityMedium:
		return "MEDIUM"
	case ast.PriorityHigh:
		return "HIGH"
	case ast.PriorityCritical:
		return "CRITICAL"
	}
	return "UNKNOWN"
}

func ruleNameList(rules []*ast.RuleBlock) string {
	out := ""
	for i, r := range rules {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%q", r.Name)
	}
	return out
}

func sortByName(rules []*ast.RuleBlock) {
	// Insertion sort — set sizes here are tiny (rules matching one target).
	for i := 1; i < len(rules); i++ {
		for j := i; j > 0 && rules[j-1].Name > rules[j].Name; j-- {
			rules[j-1], rules[j] = rules[j], rules[j-1]
		}
	}
}

package factstore

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// MemoryStore is an in-process FactStore. It holds facts in a map indexed
// by record-ID and evaluates Query values with the kind of variable
// binding any reader of mid-80s Prolog will recognise. It is the default
// backend for the REPL and the test runner, and the simplest possible
// proof that the FactStore abstraction is real: a second implementer
// alongside the Datalevin client.
//
// All methods are safe for concurrent use.
type MemoryStore struct {
	mu       sync.RWMutex
	entities map[int]map[string]any
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entities: map[int]map[string]any{}}
}

// Assert merges facts into the store. A fact's RecordID parses as an
// integer entity ID; non-integer record IDs are rejected so the store
// stays compatible with the executor's row-projection conventions.
//
// Multiple Assert calls for the same RecordID accumulate: later values
// overwrite earlier ones for the same attribute, matching the test-
// fixture semantics. Empty attribute names are skipped — they signal an
// entity declaration with no payload.
func (m *MemoryStore) Assert(ctx context.Context, facts []Fact) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, f := range facts {
		if f.RecordID == "" {
			continue
		}
		id, err := parseRecordID(f.RecordID)
		if err != nil {
			return fmt.Errorf("memorystore: assert: %w", err)
		}
		ent := m.entities[id]
		if ent == nil {
			ent = map[string]any{}
			m.entities[id] = ent
		}
		if f.Attribute != "" {
			ent[f.Attribute] = f.Value
		}
	}
	return nil
}

// Len returns the number of distinct record IDs in the store. Used by
// the REPL's `:facts` command to size its listing.
func (m *MemoryStore) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entities)
}

// Snapshot returns a deep copy of the store's contents, sorted by record
// ID for stable iteration. Useful for `:facts`-style display where the
// caller wants a consistent view independent of the underlying map.
func (m *MemoryStore) Snapshot() map[int]map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[int]map[string]any, len(m.entities))
	for id, attrs := range m.entities {
		cp := make(map[string]any, len(attrs))
		for k, v := range attrs {
			cp[k] = v
		}
		out[id] = cp
	}
	return out
}

// Reset drops all facts. Used by REPL `:clear facts`.
func (m *MemoryStore) Reset() {
	m.mu.Lock()
	m.entities = map[int]map[string]any{}
	m.mu.Unlock()
}

// Query evaluates a structured Query by iterating entities, attempting
// to satisfy the clause list against each one, and projecting the bound
// variables.
//
// The evaluation strategy is intentionally naive — one pass per entity,
// short-circuit on first failing clause. For Talon's per-tenant fact
// volumes (thousands, not millions) this is plenty; a future indexed
// MemoryStore can replace the loop without changing the interface.
func (m *MemoryStore) Query(ctx context.Context, q Query) ([][]any, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Sort entity IDs so result ordering is stable across runs — tests
	// and the REPL both rely on deterministic ordering.
	ids := sortedKeys(m.entities)

	var rows [][]any
	for _, id := range ids {
		attrs := m.entities[id]
		bindings := map[string]any{}
		// "?e" is the conventional entity binding the planner emits.
		bindings["?e"] = float64(id)
		if !matchAll(q.Where, attrs, bindings) {
			continue
		}
		row := make([]any, len(q.Find))
		for i, name := range q.Find {
			row[i] = bindings[name]
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// ─── Evaluation ──────────────────────────────────────────────────────────────

func matchAll(clauses []Clause, attrs map[string]any, bindings map[string]any) bool {
	for _, c := range clauses {
		if !matchOne(c, attrs, bindings) {
			return false
		}
	}
	return true
}

func matchOne(c Clause, attrs map[string]any, bindings map[string]any) bool {
	switch cc := c.(type) {
	case *Pattern:
		return matchPattern(cc, attrs, bindings)
	case *Predicate:
		return matchPredicate(cc, bindings)
	case *Or:
		return matchOr(cc, attrs, bindings)
	case *Not:
		return matchNot(cc, attrs, bindings)
	}
	return false
}

// matchPattern unifies a pattern with the entity's attribute map. The
// entity term is currently expected to refer to the bound "?e" (the
// planner does not emit cross-entity joins yet); the attribute is a
// concrete namespace+name; the value term binds or matches.
func matchPattern(p *Pattern, attrs map[string]any, bindings map[string]any) bool {
	if p.Attribute == "" {
		return false
	}
	val, ok := attrs[p.Attribute]
	if !ok {
		return false
	}
	if p.Value.IsWildcard() {
		return true
	}
	if p.Value.IsVar() {
		if existing, bound := bindings[p.Value.Var]; bound {
			return equalValues(existing, val)
		}
		bindings[p.Value.Var] = val
		return true
	}
	return equalValues(p.Value.Literal, val)
}

func matchPredicate(p *Predicate, bindings map[string]any) bool {
	left := resolveTerm(p.Left, bindings)
	right := resolveTerm(p.Right, bindings)
	if left == nil || right == nil {
		return false
	}
	switch p.Op {
	case "==":
		return equalValues(left, right)
	case "!=":
		return !equalValues(left, right)
	case "<", "<=", ">", ">=":
		return compareNumbers(p.Op, left, right)
	case "starts_with":
		return stringPredicate(left, right, strings.HasPrefix)
	case "ends_with":
		return stringPredicate(left, right, strings.HasSuffix)
	case "contains":
		return stringPredicate(left, right, strings.Contains)
	case "in":
		return inSet(left, right)
	case "not_in":
		return !inSet(left, right)
	}
	return false
}

// inSet reports whether `value` is one of the members in the right-hand
// set. The set arrives as a Term wrapping a []any literal — the planner
// emits this shape for `attr in ["a", "b"]` membership tests.
func inSet(value, set any) bool {
	members, ok := set.([]any)
	if !ok {
		return false
	}
	for _, m := range members {
		if equalValues(value, m) {
			return true
		}
	}
	return false
}

func matchOr(o *Or, attrs map[string]any, bindings map[string]any) bool {
	for _, branch := range o.Branches {
		// Bindings made inside an Or branch should not leak to siblings,
		// per Datalog semantics. We clone, evaluate, and on success copy
		// only the surviving branch's new bindings back into the parent.
		scratch := cloneBindings(bindings)
		if matchAll(branch, attrs, scratch) {
			for k, v := range scratch {
				if _, had := bindings[k]; !had {
					bindings[k] = v
				}
			}
			return true
		}
	}
	return false
}

func matchNot(n *Not, attrs map[string]any, bindings map[string]any) bool {
	scratch := cloneBindings(bindings)
	return !matchAll(n.Body, attrs, scratch)
}

func resolveTerm(t Term, bindings map[string]any) any {
	if t.IsVar() {
		return bindings[t.Var]
	}
	return t.Literal
}

func equalValues(a, b any) bool {
	if af, aok := toFloat(a); aok {
		if bf, bok := toFloat(b); bok {
			return af == bf
		}
	}
	return a == b
}

func compareNumbers(op string, a, b any) bool {
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if !aok || !bok {
		return false
	}
	switch op {
	case "<":
		return af < bf
	case "<=":
		return af <= bf
	case ">":
		return af > bf
	case ">=":
		return af >= bf
	}
	return false
}

func stringPredicate(a, b any, op func(string, string) bool) bool {
	as, aok := a.(string)
	bs, bok := b.(string)
	if !aok || !bok {
		return false
	}
	return op(as, bs)
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func cloneBindings(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// parseRecordID turns the RecordID string back into the integer entity
// ID the planner expects in column 0. Today every code path uses
// fmt.Sprint(id) — kept tolerant in case a future caller supplies a
// padded or signed representation.
func parseRecordID(s string) (int, error) {
	n := 0
	neg := false
	if len(s) > 0 && s[0] == '-' {
		neg = true
		s = s[1:]
	}
	if s == "" {
		return 0, fmt.Errorf("empty record ID")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("non-integer record ID %q", s)
		}
		n = n*10 + int(r-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}

func sortedKeys(m map[int]map[string]any) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Tiny inline insertion sort — keeps the dependency surface flat
	// (no `sort` import in factstore so it stays a leaf package).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

package factstore

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
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
	// updatedAt[id][attr] is the time that (entity, attribute) was last
	// asserted — updated on every Assert regardless of value change, and
	// cleared on Retract. Backs the [Freshness] capability.
	updatedAt map[int]map[string]time.Time
	now       func() time.Time
	events    EventEmitter
}

// NewMemoryStore returns an empty store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		entities:  map[int]map[string]any{},
		updatedAt: map[int]map[string]time.Time{},
		now:       time.Now,
	}
}

// SetClock overrides the clock used to stamp fact write-times. Intended
// for tests that need deterministic freshness; production leaves the
// default time.Now.
func (m *MemoryStore) SetClock(fn func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if fn == nil {
		fn = time.Now
	}
	m.now = fn
}

// LastWritten reports when the (recordID, attribute) fact was last
// asserted. ok is false when the record ID isn't an integer, the entity
// or attribute is unknown, or it was retracted. Implements [Freshness].
func (m *MemoryStore) LastWritten(recordID, attribute string) (time.Time, bool) {
	id, err := parseRecordID(recordID)
	if err != nil {
		return time.Time{}, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	attrs, ok := m.updatedAt[id]
	if !ok {
		return time.Time{}, false
	}
	t, ok := attrs[attribute]
	return t, ok
}

var _ Freshness = (*MemoryStore)(nil)

// Assert merges facts into the store. A fact's RecordID parses as an
// integer entity ID; non-integer record IDs are rejected so the store
// stays compatible with the executor's row-projection conventions.
//
// Multiple Assert calls for the same RecordID accumulate: later values
// overwrite earlier ones for the same attribute, matching the test-
// fixture semantics. Empty attribute names are skipped — they signal an
// entity declaration with no payload.
//
// For every (RecordID, Attribute) cell touched:
//
//   - if the attribute was absent, fire EventAssert with the new fact
//   - if the attribute existed with a different value, fire EventChange
//     with Prev set to the prior fact
//   - if the value is unchanged, fire nothing — Assert is idempotent
//
// Events are dispatched after the mutation is visible and outside the
// store mutex so subscribers can re-enter the store without deadlock.
func (m *MemoryStore) Assert(ctx context.Context, facts []Fact) error {
	m.mu.Lock()
	var emitted []Event
	for _, f := range facts {
		if f.RecordID == "" {
			continue
		}
		id, err := parseRecordID(f.RecordID)
		if err != nil {
			m.mu.Unlock()
			return fmt.Errorf("memorystore: assert: %w", err)
		}
		ent := m.entities[id]
		if ent == nil {
			ent = map[string]any{}
			m.entities[id] = ent
		}
		if f.Attribute == "" {
			continue
		}
		// Stamp the write-time on every assert — even an unchanged
		// re-assert counts as a refresh for freshness purposes — before
		// the idempotent short-circuit below.
		if m.updatedAt[id] == nil {
			m.updatedAt[id] = map[string]time.Time{}
		}
		m.updatedAt[id][f.Attribute] = m.now()
		if prev, had := ent[f.Attribute]; had {
			if equalValues(prev, f.Value) {
				continue // idempotent — no event
			}
			emitted = append(emitted, Event{
				Kind: EventChange,
				Fact: f,
				Prev: Fact{RecordID: f.RecordID, Attribute: f.Attribute, Value: prev},
			})
		} else {
			emitted = append(emitted, Event{
				Kind: EventAssert,
				Fact: f,
			})
		}
		ent[f.Attribute] = f.Value
	}
	m.mu.Unlock()

	// Fan-out events outside the lock so subscribers can re-enter the
	// store from their handler without deadlock — mirrors the Retract
	// emission pattern below.
	for _, ev := range emitted {
		m.events.Emit(ctx, ev)
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
	m.updatedAt = map[int]map[string]time.Time{}
	m.mu.Unlock()
}

// Retract removes cells matching the pattern. Semantics (per
// FactStore.Retract):
//
//   - empty Attribute            → drop the whole entity
//   - Attribute set, nil Value   → drop every cell with that attribute
//   - Attribute + concrete Value → drop only the cell whose value equals
//
// Each removed cell emits a Retract event so the reactive dispatcher
// can fire `on retract <type>` blocks against it.
func (m *MemoryStore) Retract(ctx context.Context, pattern RetractPattern) error {
	if pattern.RecordID == "" {
		return fmt.Errorf("memorystore: retract: RecordID is required")
	}
	id, err := parseRecordID(pattern.RecordID)
	if err != nil {
		return fmt.Errorf("memorystore: retract: %w", err)
	}

	m.mu.Lock()
	ent := m.entities[id]
	if ent == nil {
		m.mu.Unlock()
		return nil // nothing to do; idempotent
	}

	var removed []Fact
	switch {
	case pattern.Attribute == "":
		// Drop the entire entity. Collect each cell as a retraction
		// event so reactive dispatchers see one event per (attribute,
		// value) pair, not one merged event for the whole entity.
		for attr, val := range ent {
			removed = append(removed, Fact{
				RecordID:  pattern.RecordID,
				Attribute: attr,
				Value:     val,
			})
		}
		delete(m.entities, id)
	case pattern.Value == nil:
		// Drop one attribute regardless of value.
		if val, ok := ent[pattern.Attribute]; ok {
			removed = append(removed, Fact{
				RecordID:  pattern.RecordID,
				Attribute: pattern.Attribute,
				Value:     val,
			})
			delete(ent, pattern.Attribute)
			if len(ent) == 0 {
				delete(m.entities, id)
			}
		}
	default:
		// Drop one specific cell — only if the stored value matches.
		if val, ok := ent[pattern.Attribute]; ok && equalValues(val, pattern.Value) {
			removed = append(removed, Fact{
				RecordID:  pattern.RecordID,
				Attribute: pattern.Attribute,
				Value:     val,
			})
			delete(ent, pattern.Attribute)
			if len(ent) == 0 {
				delete(m.entities, id)
			}
		}
	}

	// Clear write-time stamps for every removed cell (removed lists one
	// Fact per dropped cell in all three cases).
	if stamps := m.updatedAt[id]; stamps != nil {
		for _, f := range removed {
			delete(stamps, f.Attribute)
		}
		if len(stamps) == 0 {
			delete(m.updatedAt, id)
		}
	}
	m.mu.Unlock()

	// Fan-out events outside the lock so subscribers can't deadlock by
	// re-entering the store from their handler.
	for _, f := range removed {
		m.events.Emit(ctx, Event{
			Kind: EventRetract,
			Fact: f,
		})
	}
	return nil
}

// Events returns the store's event emitter so reactive dispatchers and
// other consumers can subscribe. The emitter fires on Assert (new fact
// or value change) and Retract; see EventKind for the variants.
func (m *MemoryStore) Events() *EventEmitter {
	return &m.events
}

// Query evaluates a structured Query by iterating entities, attempting
// to satisfy the clause list against each one, and projecting the bound
// variables.
//
// The evaluation strategy is intentionally naive — one pass per entity,
// short-circuit on first failing clause. For Talon's per-tenant fact
// volumes (thousands, not millions) this is plenty; a future indexed
// MemoryStore can replace the loop without changing the interface.
//
// When q.Aggregates is non-empty, the projection step is replaced by
// aggregation — see runAggregates.
func (m *MemoryStore) Query(ctx context.Context, q Query) ([][]any, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Sort entity IDs so result ordering is stable across runs — tests
	// and the REPL both rely on deterministic ordering.
	ids := sortedKeys(m.entities)

	// Build a rule-resolution context only when the query carries
	// rules. Resolution is lazy / per-call-site so caller-side literal
	// args flow into the rule body — required for the base case of
	// transitive-closure rules like category-in-tree.
	var rc *ruleCtx
	if len(q.Rules) > 0 {
		rc = newRuleCtx(m, q.Rules)
	}

	// Pass 1: collect every matching binding set.
	var matches []map[string]any
	for _, id := range ids {
		attrs := m.entities[id]
		bindings := map[string]any{}
		// "?e" is the conventional entity binding the planner emits.
		bindings["?e"] = float64(id)
		if !matchAllWithRules(q.Where, attrs, bindings, rc) {
			continue
		}
		matches = append(matches, bindings)
	}

	// Pass 2: project or aggregate.
	if len(q.Aggregates) > 0 {
		return runAggregates(matches, q.GroupBy, q.Aggregates), nil
	}

	rows := make([][]any, 0, len(matches))
	for _, b := range matches {
		row := make([]any, len(q.Find))
		for i, name := range q.Find {
			row[i] = b[name]
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// runAggregates groups matched binding sets by GroupBy and computes each
// Aggregate. The result row layout is [group-by columns..., aggregate
// columns...] — same order as Query.String() emits.
//
// Groups are visited in lexicographic order of their group-by key so the
// output is deterministic across runs (helpful for tests and REPL).
func runAggregates(matches []map[string]any, groupBy []string, aggs []Aggregate) [][]any {
	if len(groupBy) == 0 {
		// Single group: one row with one column per aggregate.
		row := make([]any, len(aggs))
		for i, a := range aggs {
			row[i] = computeAggregate(a, matches)
		}
		return [][]any{row}
	}

	// Bucket by composite group key.
	type bucket struct {
		key     []any
		members []map[string]any
	}
	buckets := map[string]*bucket{}
	order := []string{}
	for _, b := range matches {
		key := make([]any, len(groupBy))
		for i, v := range groupBy {
			key[i] = b[v]
		}
		k := groupKeyString(key)
		if _, ok := buckets[k]; !ok {
			buckets[k] = &bucket{key: key}
			order = append(order, k)
		}
		buckets[k].members = append(buckets[k].members, b)
	}

	// Stable sort by the composite key string.
	insertionSortStrings(order)

	rows := make([][]any, 0, len(order))
	for _, k := range order {
		bk := buckets[k]
		row := make([]any, 0, len(groupBy)+len(aggs))
		row = append(row, bk.key...)
		for _, a := range aggs {
			row = append(row, computeAggregate(a, bk.members))
		}
		rows = append(rows, row)
	}
	return rows
}

func computeAggregate(a Aggregate, members []map[string]any) any {
	switch a.Fn {
	case "count":
		return float64(len(members))
	case "sum", "total":
		s, _ := sumOver(a.Over, members)
		return s
	case "avg":
		s, n := sumOver(a.Over, members)
		if n == 0 {
			return float64(0)
		}
		return s / float64(n)
	case "min":
		return minOver(a.Over, members)
	case "max":
		return maxOver(a.Over, members)
	}
	return nil
}

func sumOver(t Term, members []map[string]any) (float64, int) {
	if t.Var == "" {
		return 0, 0
	}
	var sum float64
	n := 0
	for _, b := range members {
		if f, ok := toFloat(b[t.Var]); ok {
			sum += f
			n++
		}
	}
	return sum, n
}

func minOver(t Term, members []map[string]any) any {
	if t.Var == "" {
		return nil
	}
	var best float64
	seen := false
	for _, b := range members {
		f, ok := toFloat(b[t.Var])
		if !ok {
			continue
		}
		if !seen || f < best {
			best = f
			seen = true
		}
	}
	if !seen {
		return nil
	}
	return best
}

func maxOver(t Term, members []map[string]any) any {
	if t.Var == "" {
		return nil
	}
	var best float64
	seen := false
	for _, b := range members {
		f, ok := toFloat(b[t.Var])
		if !ok {
			continue
		}
		if !seen || f > best {
			best = f
			seen = true
		}
	}
	if !seen {
		return nil
	}
	return best
}

func groupKeyString(key []any) string {
	var b strings.Builder
	for i, v := range key {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(fmt.Sprintf("%v", v))
	}
	return b.String()
}

func insertionSortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
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
	return matchOneWithRules(c, attrs, bindings, nil)
}

func matchAllWithRules(clauses []Clause, attrs map[string]any, bindings map[string]any, rc *ruleCtx) bool {
	for _, c := range clauses {
		if !matchOneWithRules(c, attrs, bindings, rc) {
			return false
		}
	}
	return true
}

func matchOneWithRules(c Clause, attrs map[string]any, bindings map[string]any, rc *ruleCtx) bool {
	switch cc := c.(type) {
	case *Pattern:
		return matchPattern(cc, attrs, bindings)
	case *Predicate:
		return matchPredicate(cc, bindings)
	case *Or:
		return matchOr(cc, attrs, bindings)
	case *Not:
		return matchNot(cc, attrs, bindings)
	case *FullText:
		return matchFullText(cc, attrs)
	case *RuleCall:
		return matchRuleCall(cc, bindings, rc)
	}
	return false
}

// matchFullText is MemoryStore's naive fallback for Datalevin's
// `(fulltext $ "query")` predicate: it scans every string-valued
// attribute on the entity and reports a match if any value contains
// the query as a case-insensitive substring. A backend with real
// FTS indices (Datalevin's `:db/fulltext true`) will out-perform
// this trivially, but the fallback is enough for the REPL and
// MemoryStore-backed tests.
func matchFullText(f *FullText, attrs map[string]any) bool {
	if f.Query == "" {
		return false
	}
	needle := strings.ToLower(f.Query)
	for _, v := range attrs {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if strings.Contains(strings.ToLower(s), needle) {
			return true
		}
	}
	return false
}

// matchRuleCall resolves a RuleCall lazily: it asks the ruleCtx for
// the tuple set matching the call's currently-bound args (literals
// and bound vars flow into the rule body as seeds), then unifies
// each returned tuple against the call's terms.
func matchRuleCall(call *RuleCall, bindings map[string]any, rc *ruleCtx) bool {
	if rc == nil {
		return false
	}
	seed := make([]any, len(call.Args))
	for i, arg := range call.Args {
		switch {
		case arg.IsVar():
			if v, ok := bindings[arg.Var]; ok {
				seed[i] = v
			}
		case arg.IsWildcard():
			// leave free
		default:
			seed[i] = arg.Literal
		}
	}
	tuples := rc.resolve(call.Name, seed)
	for _, t := range tuples {
		if len(t) != len(call.Args) {
			continue
		}
		scratch := cloneBindings(bindings)
		unified := true
		for i, arg := range call.Args {
			if !unifyTerm(arg, t[i], scratch) {
				unified = false
				break
			}
		}
		if unified {
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

package talondb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opentalon/talon-language/internal/factstore"
	pb "github.com/opentalon/talon-db/proto/talondbpb"
)

// ruleResolution drives RuleCall evaluation in the adapter.
//
// The adapter supports exactly one rule shape today: the standard
// category-in-tree recursion (transitive closure over
// `:category/parent`). The MemoryStore reference uses semi-naive
// fixed-point evaluation; the adapter short-circuits to talon-db's
// pre-computed closure table via the Descendants / Ancestors RPCs,
// which is dramatically faster on deep hierarchies and matches the
// Datalevin path's behaviour from the consumer's point of view.
//
// Other recursive rule shapes — arbitrary user-defined recursion,
// rules without a :category/parent step — return ErrUnsupported with
// a clear message. Talon-language's MemoryStore covers those today;
// users who need them stay on the in-memory or Datalevin backends
// until the adapter grows a general semi-naive evaluator.
type ruleResolution struct {
	client *Client
	rules  []factstore.Rule
	// allowedByCall caches resolved value sets keyed on the call's
	// stringified args. Avoids re-issuing Descendants for repeat calls
	// inside one Query.
	allowedByCall map[string]map[any]bool
}

func newRuleResolution(client *Client, rules []factstore.Rule) *ruleResolution {
	return &ruleResolution{
		client:        client,
		rules:         rules,
		allowedByCall: map[string]map[any]bool{},
	}
}

// preResolve walks the Where slice (including nested Or/Not branches)
// and resolves every RuleCall it finds. Returns the first
// unsupported-shape error encountered; otherwise populates the cache
// so matchRuleCall can answer synchronously during the candidate
// loop.
func (r *ruleResolution) preResolve(ctx context.Context, clauses []factstore.Clause) error {
	for _, c := range clauses {
		switch cc := c.(type) {
		case *factstore.RuleCall:
			if _, err := r.resolve(ctx, cc); err != nil {
				return err
			}
		case *factstore.Or:
			for _, branch := range cc.Branches {
				if err := r.preResolve(ctx, branch); err != nil {
					return err
				}
			}
		case *factstore.Not:
			if err := r.preResolve(ctx, cc.Body); err != nil {
				return err
			}
		}
	}
	return nil
}

// cachedAllowed returns the resolver's pre-built allowed-set for the
// given call. Second return is false when the call was never
// resolved (caller can fail closed).
func (r *ruleResolution) cachedAllowed(call *factstore.RuleCall) (map[any]bool, bool) {
	set, ok := r.allowedByCall[ruleCallKey(call)]
	return set, ok
}

// hasRuleCall reports whether any clause (or nested clause) is a
// RuleCall. Lets Adapter.Query skip resolver setup when the query
// doesn't reference rules at all.
func hasRuleCall(clauses []factstore.Clause) bool {
	for _, c := range clauses {
		switch cc := c.(type) {
		case *factstore.RuleCall:
			return true
		case *factstore.Or:
			for _, branch := range cc.Branches {
				if hasRuleCall(branch) {
					return true
				}
			}
		case *factstore.Not:
			if hasRuleCall(cc.Body) {
				return true
			}
		}
	}
	return false
}

// resolve returns the set of values the call's first (bound-var)
// argument may take. The second argument must be a literal — the root
// of the tree to enumerate. Returns ErrUnsupported for any shape the
// adapter can't handle.
func (r *ruleResolution) resolve(ctx context.Context, call *factstore.RuleCall) (map[any]bool, error) {
	key := ruleCallKey(call)
	if cached, ok := r.allowedByCall[key]; ok {
		return cached, nil
	}
	if len(call.Args) != 2 {
		return nil, ruleUnsupported(call, "category-in-tree rules take exactly 2 arguments (?var, root)")
	}
	if !call.Args[0].IsVar() {
		return nil, ruleUnsupported(call, "first argument must be a variable")
	}
	rootTerm := call.Args[1]
	if rootTerm.Literal == nil {
		return nil, ruleUnsupported(call, "second argument must be a literal root value")
	}
	rules := rulesByName(r.rules, call.Name)
	if !isCategoryInTreeShape(rules) {
		return nil, ruleUnsupported(call, "only category-in-tree-shaped rules are supported by the talon-db adapter today")
	}

	rootValue := rootTerm.Literal
	allowed, err := r.descendantNames(ctx, rootValue)
	if err != nil {
		return nil, err
	}
	r.allowedByCall[key] = allowed
	return allowed, nil
}

// descendantNames returns the set of `:category/name` values that
// satisfy `category-in-tree(?c, root)` — i.e. root itself plus every
// category whose ancestor chain reaches root. Issues:
//   - One Query to find the docID of the category named `root`.
//   - One Descendants RPC to enumerate descendant docIDs.
//   - One Query per descendant docID to resolve back to its
//     `:category/name`. Could be batched in a future PR; for the
//     tree sizes talon-language consumers exercise (~dozens), the
//     per-doc round-trip is fine.
func (r *ruleResolution) descendantNames(ctx context.Context, root any) (map[any]bool, error) {
	out := map[any]bool{root: true}

	rootDocID, err := r.findCategoryDocID(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("talondb adapter: rule resolve: locate root category: %w", err)
	}
	if rootDocID == "" {
		// Root category not present in the store — only the literal
		// root itself counts (caller may still expect that).
		return out, nil
	}
	resp, err := r.client.svc.Descendants(ctx, &pb.DescendantsRequest{
		EntityId: r.client.Tenant(),
		RootId:   rootDocID,
	})
	if err != nil {
		return nil, fmt.Errorf("talondb adapter: rule resolve: Descendants: %w", err)
	}
	for _, descDocID := range resp.GetDocIds() {
		name, err := r.findCategoryName(ctx, descDocID)
		if err != nil {
			return nil, fmt.Errorf("talondb adapter: rule resolve: resolve descendant %q: %w", descDocID, err)
		}
		if name != "" {
			out[name] = true
		}
	}
	return out, nil
}

// findCategoryDocID returns the docID of the category record whose
// :category/name equals `name`. Empty string when none exists.
func (r *ruleResolution) findCategoryDocID(ctx context.Context, name any) (string, error) {
	term := composeRuleTerm(":category/name", name)
	resp, err := r.client.svc.Lookup(ctx, &pb.LookupRequest{
		EntityId: r.client.Tenant(),
		Term:     term,
	})
	if err != nil {
		return "", err
	}
	ids := resp.GetDocIds()
	if len(ids) == 0 {
		return "", nil
	}
	return ids[0], nil // category names are unique by convention
}

// findCategoryName fetches doc.<:category/name> for the given docID.
// Returns "" if the doc is missing or has no name attribute.
func (r *ruleResolution) findCategoryName(ctx context.Context, docID string) (string, error) {
	doc, err := r.adapterFetchDoc(ctx, docID)
	if err != nil {
		return "", err
	}
	if v, ok := doc[":category/name"]; ok {
		if s, ok := v.(string); ok {
			return s, nil
		}
	}
	return "", nil
}

// adapterFetchDoc is a thin shim around the gRPC Get so the resolver
// can fetch without an Adapter reference. Returns an empty map when
// the doc is missing.
func (r *ruleResolution) adapterFetchDoc(ctx context.Context, docID string) (map[string]any, error) {
	resp, err := r.client.svc.Get(ctx, &pb.GetRequest{
		EntityId: r.client.Tenant(),
		DocId:    docID,
	})
	if err != nil {
		return nil, err
	}
	if !resp.GetFound() {
		return map[string]any{}, nil
	}
	out := map[string]any{}
	if err := json.Unmarshal(resp.GetDoc(), &out); err != nil {
		return nil, fmt.Errorf("decode descendant doc %q: %w", docID, err)
	}
	return out, nil
}

// ---------- shape detection ----------

// isCategoryInTreeShape recognises the canonical 2-rule recursion
// over :category/parent. Conservative — requires:
//
//   - exactly 2 rule definitions sharing the same Name
//   - one rule with a `:category/parent` Pattern + recursive RuleCall
//   - one rule WITHOUT a `:category/parent` Pattern (the base case)
//
// Doesn't deep-check predicate shapes; treats anything that walks
// :category/parent and recurses with the same head name as "category
// in tree".
func isCategoryInTreeShape(rules []factstore.Rule) bool {
	if len(rules) != 2 {
		return false
	}
	hasBase, hasStep := false, false
	for _, r := range rules {
		if ruleBodyHasParentRecursion(r) {
			hasStep = true
		} else {
			hasBase = true
		}
	}
	return hasBase && hasStep
}

func ruleBodyHasParentRecursion(r factstore.Rule) bool {
	sawParent := false
	sawRecursiveCall := false
	for _, c := range r.Body {
		if p, ok := c.(*factstore.Pattern); ok && p.Attribute == ":category/parent" {
			sawParent = true
		}
		if rc, ok := c.(*factstore.RuleCall); ok && rc.Name == r.Name {
			sawRecursiveCall = true
		}
	}
	return sawParent && sawRecursiveCall
}

func rulesByName(rules []factstore.Rule, name string) []factstore.Rule {
	var out []factstore.Rule
	for _, r := range rules {
		if r.Name == name {
			out = append(out, r)
		}
	}
	return out
}

// ---------- helpers ----------

func ruleUnsupported(call *factstore.RuleCall, why string) error {
	return fmt.Errorf("talondb adapter: RuleCall %q unsupported: %s", call.Name, why)
}

func ruleCallKey(call *factstore.RuleCall) string {
	key := call.Name
	for _, a := range call.Args {
		switch {
		case a.IsVar():
			key += "|?" + a.Var
		case a.Literal != nil:
			key += fmt.Sprintf("|=%v", a.Literal)
		default:
			key += "|_"
		}
	}
	return key
}

func composeRuleTerm(attr string, value any) string {
	return composeTerm(attr, value)
}

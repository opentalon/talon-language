package talondb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/constraints"
	"github.com/opentalon/talon-language/internal/factstore"
	pb "github.com/opentalon/talon-db/proto/talondbpb"
)

// Adapter implements factstore.FactStore against a remote talondb-server.
//
// The Query evaluator is a hybrid two-phase pass:
//
//  1. **Narrow** — every top-level Pattern with literal attribute +
//     literal value contributes a docID bitmap via Lookup. The bitmaps
//     are intersected; the survivors are the candidate set.
//  2. **Evaluate** — each candidate document is fetched once, decoded
//     into a map[string]any, and every clause in the query is matched
//     against it. Variable bindings (?e, ?attr-name, etc.) are
//     produced here; Predicates / Or / Not / FullText are evaluated
//     against the in-memory representation.
//
// This mirrors the talon-language MemoryStore solver while delegating
// the candidate-set narrowing to the server's indexes.
type Adapter struct {
	client      *Client
	events      *adapterEvents // pointer so clones (WithConstraints) share the emitter
	constraints []*ast.ConstraintBlock
}

// New wraps a Client in a FactStore implementation.
func New(client *Client) *Adapter {
	return &Adapter{client: client, events: &adapterEvents{}}
}

// WithConstraints returns a clone of the adapter that evaluates the
// given constraint blocks against every Assert. Records whose merged
// state violates a constraint with Mode = "reject" are not written —
// Assert returns a typed ConstraintViolationError naming the constraint
// and reason. "warn" and "quarantine" verdicts are recorded but the
// write proceeds; callers that care can inspect the violations via
// the error.
//
// Constraint blocks are typically parsed from the .talon source by
// the compiler and threaded through here from cmd/talon's wiring.
func (a *Adapter) WithConstraints(blocks []*ast.ConstraintBlock) *Adapter {
	clone := *a
	clone.constraints = blocks
	return &clone
}

// ConstraintViolationError is returned by Assert / Retract when a
// constraint with Mode = "reject" fires against the prospective state.
// Reasons aggregates the human-readable messages from every rejecting
// constraint.
type ConstraintViolationError struct {
	RecordID string
	Reasons  []string
}

func (e *ConstraintViolationError) Error() string {
	if len(e.Reasons) == 0 {
		return fmt.Sprintf("talondb adapter: constraint violation on %q", e.RecordID)
	}
	return fmt.Sprintf("talondb adapter: constraint violation on %q: %s",
		e.RecordID, strings.Join(e.Reasons, "; "))
}

var _ factstore.FactStore = (*Adapter)(nil)

// ---------- Assert ----------

// Assert groups facts by RecordID, merges existing JSON docs for each
// record with the incoming attributes, and Puts the result. Multiple
// Asserts of the same RecordID accumulate; later values overwrite
// earlier ones for the same attribute.
func (a *Adapter) Assert(ctx context.Context, facts []factstore.Fact) error {
	byRecord := map[string]map[string]any{}
	for _, f := range facts {
		if f.RecordID == "" || f.Attribute == "" {
			continue
		}
		row, ok := byRecord[f.RecordID]
		if !ok {
			row = map[string]any{}
			byRecord[f.RecordID] = row
		}
		row[f.Attribute] = f.Value
	}
	if len(byRecord) == 0 {
		return nil
	}
	tenant := a.client.Tenant()
	for recordID, attrs := range byRecord {
		existing, err := a.fetchDoc(ctx, tenant, recordID)
		if err != nil {
			return fmt.Errorf("talondb adapter: Assert read %q: %w", recordID, err)
		}
		for k, v := range attrs {
			existing[k] = v
		}
		if err := a.checkConstraints(recordID, existing); err != nil {
			return err
		}
		raw, err := json.Marshal(existing)
		if err != nil {
			return fmt.Errorf("talondb adapter: Assert encode %q: %w", recordID, err)
		}
		if _, err := a.client.svc.Put(ctx, &pb.PutRequest{
			EntityId: tenant,
			DocId:    recordID,
			Doc:      raw,
		}); err != nil {
			return fmt.Errorf("talondb adapter: Assert Put %q: %w", recordID, err)
		}
	}
	return nil
}

// checkConstraints runs the configured constraint blocks against the
// merged prospective record state. Returns a ConstraintViolationError
// when the combined verdict is "reject"; warn / quarantine verdicts
// don't block the write (they're observable via future telemetry, but
// silent at the gate today — matching the design in
// internal/constraints/constraints.go's Verdict semantics).
//
// The merged record uses talon-language's namespaced attribute keys
// (":record/type", ":attr/km", ...); the constraint evaluator expects
// bare keys ("type", "km", ...). bareKeyView builds a temporary view
// over the record with the namespace stripped, so .talon-authored
// constraints continue to read as the language spec intends.
func (a *Adapter) checkConstraints(recordID string, record map[string]any) error {
	if len(a.constraints) == 0 {
		return nil
	}
	bare := bareKeyView(record)
	verdict := constraints.Check(bare, a.constraints)
	if verdict.Mode == "reject" {
		return &ConstraintViolationError{
			RecordID: recordID,
			Reasons:  append([]string(nil), verdict.Reasons...),
		}
	}
	return nil
}

// bareKeyView returns a copy of in with namespaced attribute keys
// reduced to their last path segment: ":record/type" → "type",
// ":attr/current_stock" → "current_stock", "name" → "name". When two
// namespaced keys collide on the same bare name, last-write-wins is
// the documented behaviour — collisions are rare in practice because
// talon-language's planner emits one namespace per attribute family.
func bareKeyView(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[stripNamespace(k)] = v
	}
	return out
}

func stripNamespace(k string) string {
	if i := strings.LastIndexByte(k, '/'); i >= 0 && i < len(k)-1 {
		return k[i+1:]
	}
	if strings.HasPrefix(k, ":") {
		return k[1:]
	}
	return k
}

// fetchDoc returns the JSON-decoded body for (tenant, recordID), or
// an empty map if the doc does not yet exist.
func (a *Adapter) fetchDoc(ctx context.Context, tenant, recordID string) (map[string]any, error) {
	resp, err := a.client.svc.Get(ctx, &pb.GetRequest{EntityId: tenant, DocId: recordID})
	if err != nil {
		return nil, err
	}
	if !resp.GetFound() {
		return map[string]any{}, nil
	}
	out := map[string]any{}
	if err := json.Unmarshal(resp.GetDoc(), &out); err != nil {
		return nil, fmt.Errorf("decode existing doc: %w", err)
	}
	return out, nil
}

// ---------- Retract ----------

// Retract today supports the RecordID-only shape (delete the entire
// document). Attribute / Value-scoped retract is a follow-up.
func (a *Adapter) Retract(ctx context.Context, p factstore.RetractPattern) error {
	if p.RecordID == "" {
		return errors.New("talondb adapter: Retract requires RecordID")
	}
	if p.Attribute != "" || p.Value != nil {
		return errors.ErrUnsupported
	}
	_, err := a.client.svc.Delete(ctx, &pb.DeleteRequest{
		EntityId: a.client.Tenant(),
		DocId:    p.RecordID,
	})
	return err
}

// ---------- Query ----------

// Query implements the hybrid index+eval strategy described in the
// package doc-comment.
func (a *Adapter) Query(ctx context.Context, q factstore.Query) ([][]any, error) {
	if len(q.Aggregates) > 0 || len(q.Pull) > 0 || len(q.Rules) > 0 {
		return nil, errors.ErrUnsupported
	}

	tenant := a.client.Tenant()
	anchors := collectAnchors(q.Where)
	if len(anchors) == 0 {
		// We could fall back to a full scan via Lookup("") + Get; for
		// now match Datalevin's behaviour and reject — every query the
		// planner emits today has at least one literal anchor.
		return nil, fmt.Errorf("talondb adapter: query has no anchor pattern (literal attr + literal value)")
	}

	candidates, err := a.gatherCandidates(ctx, tenant, anchors)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	rows := make([][]any, 0, len(candidates))
	for _, docID := range candidates {
		doc, err := a.fetchDoc(ctx, tenant, docID)
		if err != nil {
			return nil, fmt.Errorf("talondb adapter: fetch %q: %w", docID, err)
		}
		bindings := map[string]any{"?e": parseRecordIDOrString(docID)}
		if !matchAll(q.Where, doc, bindings) {
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

// collectAnchors returns every top-level Pattern with a literal
// attribute AND a literal value. Patterns inside Or / Not are not
// anchors (their semantics are different).
func collectAnchors(clauses []factstore.Clause) []*factstore.Pattern {
	var out []*factstore.Pattern
	for _, c := range clauses {
		if p, ok := c.(*factstore.Pattern); ok && p.Attribute != "" && p.Value.Literal != nil {
			out = append(out, p)
		}
	}
	return out
}

// gatherCandidates Lookups each anchor's bitmap and intersects them.
func (a *Adapter) gatherCandidates(ctx context.Context, tenant string, anchors []*factstore.Pattern) ([]string, error) {
	var candidates []string
	for i, p := range anchors {
		term := composeTerm(p.Attribute, p.Value.Literal)
		resp, err := a.client.svc.Lookup(ctx, &pb.LookupRequest{
			EntityId: tenant,
			Term:     term,
		})
		if err != nil {
			return nil, fmt.Errorf("talondb adapter: Lookup %q: %w", term, err)
		}
		got := resp.GetDocIds()
		sort.Strings(got)
		if i == 0 {
			candidates = got
		} else {
			candidates = intersectSorted(candidates, got)
		}
		if len(candidates) == 0 {
			return nil, nil
		}
	}
	return candidates, nil
}

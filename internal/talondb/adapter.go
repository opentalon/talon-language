package talondb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

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
	client *Client
}

// New wraps a Client in a FactStore implementation.
func New(client *Client) *Adapter {
	return &Adapter{client: client}
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

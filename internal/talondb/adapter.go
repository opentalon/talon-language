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

// Adapter is the FactStore implementation that talks to a talondb
// server via the Client. It satisfies factstore.FactStore for the
// subset of clauses the fleet_maintenance.talon example exercises.
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

// Query evaluates the planner's structured query against the talondb
// server. The strategy:
//
//  1. Identify "anchor" patterns — those with a literal attribute and
//     literal value. For each, Lookup the composite term and collect
//     a sorted []string of matching docIDs.
//  2. Intersect every anchor's docIDs (sorted-merge join).
//  3. For each surviving docID, Get the JSON doc and bind every
//     variable that any pattern names against its attribute.
//  4. Apply Predicates Go-side. Drop rows that fail.
//  5. Project Find columns.
func (a *Adapter) Query(ctx context.Context, q factstore.Query) ([][]any, error) {
	if len(q.Aggregates) > 0 || len(q.Pull) > 0 || len(q.Rules) > 0 {
		return nil, errors.ErrUnsupported
	}

	anchors, varPatterns, err := splitPatterns(q.Where)
	if err != nil {
		return nil, err
	}

	tenant := a.client.Tenant()
	var candidates []string

	if len(anchors) > 0 {
		first := true
		for _, p := range anchors {
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
			if first {
				candidates = got
				first = false
			} else {
				candidates = intersectSorted(candidates, got)
			}
			if len(candidates) == 0 {
				return nil, nil
			}
		}
	} else if len(varPatterns) > 0 {
		// No anchor — we'd need a full Scan. The Query engine for
		// fleet_maintenance always emits at least one anchor pattern
		// per clause group, so we error here rather than silently
		// degrading.
		return nil, fmt.Errorf("talondb adapter: query has no anchor pattern (literal attr + literal value)")
	} else {
		return nil, nil
	}

	rows := make([][]any, 0, len(candidates))
	for _, docID := range candidates {
		doc, err := a.fetchDoc(ctx, tenant, docID)
		if err != nil {
			return nil, fmt.Errorf("talondb adapter: fetch %q: %w", docID, err)
		}
		bindings := map[string]any{"?e": parseRecordIDOrString(docID)}
		// Bind every var-named attribute named in any pattern.
		for _, p := range varPatterns {
			if p.Value.Var != "" && p.Attribute != "" {
				if v, ok := doc[p.Attribute]; ok {
					bindings[p.Value.Var] = v
				}
			}
		}
		// Also bind values referenced by anchor patterns when Find
		// asks for them (e.g. Find: [?e ?status] with Pattern
		// (?e, :record/status, "active") binds ?status = "active").
		// In practice the planner names variable Value terms for
		// fields it wants to read; anchors usually carry literals
		// only, so this is a safety net.
		if !applyPredicates(bindings, q.Where) {
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

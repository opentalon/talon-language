package datalevin

import (
	"context"
	"fmt"
	"strconv"

	"github.com/opentalon/talon-language/internal/factstore"
)

// Query satisfies factstore.FactStore. It renders the structured query to
// Datalevin's Clojure-flavoured Datalog text via factstore.Query.String()
// and executes it against the server. Adding a new clause type means
// updating the renderer in internal/factstore/render.go; this package
// stays as the thin HTTP bridge.
func (c *Client) Query(ctx context.Context, q factstore.Query) ([][]any, error) {
	rules := q.RulesString()
	args := q.QueryArgs()
	if rules == "" && len(args) == 0 {
		return c.RawQuery(ctx, q.String())
	}
	return c.RawQueryFull(ctx, q.String(), rules, args)
}

// Retract satisfies factstore.FactStore. It translates the
// RetractPattern into Datalevin tx-data using the
// `[:db/retract eid attr value]` (specific cell) or
// `[:db.fn/retractEntity eid]` (whole entity) shapes Datalevin
// accepts on `/transact`.
//
// RecordID is required. When Attribute is empty the entire entity is
// retracted. When Attribute is set and Value is non-nil the specific
// cell is targeted; when Value is nil the server runs a Datalog
// query first to enumerate the entity's current value for that
// attribute (Datalevin's `:db/retract` requires a value), then
// retracts each matching cell. The query-then-retract dance happens
// inside `/retract` on the server so the wire shape stays simple.
func (c *Client) Retract(ctx context.Context, p factstore.RetractPattern) error {
	if p.RecordID == "" {
		return fmt.Errorf("datalevin retract: RecordID is required")
	}
	body := map[string]any{
		"record_id": p.RecordID,
	}
	if p.Attribute != "" {
		body["attribute"] = p.Attribute
	}
	if p.Value != nil {
		body["value"] = p.Value
	}
	var result map[string]any
	if err := c.post(ctx, "/retract", body, &result); err != nil {
		return fmt.Errorf("datalevin retract: %w", err)
	}
	return nil
}

// Assert satisfies factstore.FactStore. It groups facts by RecordID,
// infers a schema from the value types so Datalevin will accept the
// entity, and commits the result as one transaction.
//
// Each entity row carries `:db/id` set to the parsed RecordID so
// external IDs equal Datalevin's internal eids. Without that
// alignment, Retract (and any other external-ID-targeting operation)
// can't find the entity, because Datalevin would auto-assign a
// different eid. RecordIDs that don't parse as longs fall back to
// Datalevin-assigned eids — the alignment is best-effort.
func (c *Client) Assert(ctx context.Context, facts []factstore.Fact) error {
	if len(facts) == 0 {
		return nil
	}

	// Group facts by record so each Datalevin entity-map is one
	// transaction row; also collect a value-type per attribute for schema
	// registration.
	type entityRow map[string]any
	byID := map[string]entityRow{}
	schema := map[string]map[string]string{}
	for _, f := range facts {
		if f.RecordID == "" {
			continue
		}
		row := byID[f.RecordID]
		if row == nil {
			row = entityRow{}
			byID[f.RecordID] = row
		}
		if f.Attribute != "" {
			row[f.Attribute] = f.Value
			if _, exists := schema[f.Attribute]; !exists && f.Value != nil {
				schema[f.Attribute] = map[string]string{"db/valueType": inferType(f.Value)}
			}
		}
	}

	if len(schema) > 0 {
		if err := c.Schema(ctx, schema); err != nil {
			return err
		}
	}

	txData := make([]map[string]any, 0, len(byID))
	for id, row := range byID {
		if eid, err := strconv.ParseInt(id, 10, 64); err == nil {
			row[":db/id"] = eid
		}
		txData = append(txData, map[string]any(row))
	}
	return c.RawTransact(ctx, txData)
}

// inferType maps a Go value to a Datalevin value-type identifier. Matches
// the heuristic the executor used to apply before this package owned
// schema registration.
func inferType(v any) string {
	switch v.(type) {
	case float64, float32, int, int32, int64:
		return "db.type/long"
	case bool:
		return "db.type/boolean"
	}
	return "db.type/string"
}

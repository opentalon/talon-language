package datalevin

import (
	"context"

	"github.com/opentalon/talon-language/internal/factstore"
)

// Query satisfies factstore.FactStore. It renders the structured query to
// Datalevin's Clojure-flavoured Datalog text via factstore.Query.String()
// and executes it against the server. Adding a new clause type means
// updating the renderer in internal/factstore/render.go; this package
// stays as the thin HTTP bridge.
func (c *Client) Query(ctx context.Context, q factstore.Query) ([][]any, error) {
	return c.RawQuery(ctx, q.String())
}

// Assert satisfies factstore.FactStore. It groups facts by RecordID,
// infers a schema from the value types so Datalevin will accept the
// entity, and commits the result as one transaction.
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
	for _, row := range byID {
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

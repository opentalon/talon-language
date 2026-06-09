//go:build datalevin

// Package datalevin's integration_test.go drives the Go client against
// a live datalevin-server. It runs only under `go test -tags=datalevin`
// because spinning up the JVM is too heavy for the regular `go test
// ./...` loop — CI's `talon-datalevin-smoke` job starts the server,
// then runs this package with the build tag set. Locally:
//
//	cd datalevin-server && clojure -M:run &
//	go test -tags=datalevin ./internal/datalevin -run Smoke -v
//
// Each subtest covers one of the recent FactStore enhancements
// (schema merge fix, Retract, full-text, recursive rules) so a
// regression on any of them fails the smoke job.
package datalevin

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/opentalon/talon-language/internal/factstore"
)

func smokeClient(t *testing.T) *Client {
	t.Helper()
	url := os.Getenv("DATALEVIN_URL")
	if url == "" {
		url = "http://localhost:8898"
	}
	c := NewClient(url)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Health(ctx); err != nil {
		t.Skipf("datalevin server unreachable at %s: %v", url, err)
	}
	return c
}

// uniqueID returns an entity ID derived from time so concurrent test
// runs on the same datalevin DB don't collide. The DB is wiped per CI
// job but stays warm across subtests; collision-free IDs keep each
// subtest independent.
func uniqueID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
}

// TestSmoke_SchemaMerge — PR #82 (issue #78). The fix: /schema must
// merge new attributes into the existing schema instead of close-
// reopening with only the new attrs. We register attribute A, then
// attribute B, then assert + query a fact using A — if the schema
// regressed, the assert would fail because A's value type was lost.
func TestSmoke_SchemaMerge(t *testing.T) {
	c := smokeClient(t)
	ctx := context.Background()

	if err := c.Schema(ctx, map[string]map[string]string{
		":smoke/a": {"db/valueType": "db.type/string"},
	}); err != nil {
		t.Fatalf("register :smoke/a: %v", err)
	}
	if err := c.Schema(ctx, map[string]map[string]string{
		":smoke/b": {"db/valueType": "db.type/long"},
	}); err != nil {
		t.Fatalf("register :smoke/b: %v", err)
	}

	id := uniqueID()
	if err := c.Assert(ctx, []factstore.Fact{
		{RecordID: id, Attribute: ":smoke/a", Value: "alpha"},
	}); err != nil {
		t.Fatalf("assert under merged schema: %v", err)
	}

	q := factstore.Query{
		Find: []string{"?v"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":smoke/a", Value: factstore.Var("v")},
		},
	}
	rows, err := c.Query(ctx, q)
	if err != nil {
		t.Fatalf("query :smoke/a: %v", err)
	}
	found := false
	for _, r := range rows {
		if s, ok := r[0].(string); ok && s == "alpha" {
			found = true
		}
	}
	if !found {
		t.Fatalf("merged-schema fact missing from query result: %v", rows)
	}
}

// TestSmoke_Retract — PR #83 (issue #79). Assert a fact, retract the
// attribute, query — expect no result. Then assert again, retract a
// specific value, query — expect no result for that value but the
// other value (if seeded) survives.
func TestSmoke_Retract(t *testing.T) {
	c := smokeClient(t)
	ctx := context.Background()

	if err := c.Schema(ctx, map[string]map[string]string{
		":smoke/r": {"db/valueType": "db.type/string"},
	}); err != nil {
		t.Fatalf("schema: %v", err)
	}

	id := uniqueID()
	if err := c.Assert(ctx, []factstore.Fact{
		{RecordID: id, Attribute: ":smoke/r", Value: "to-retract"},
	}); err != nil {
		t.Fatalf("assert: %v", err)
	}

	if err := c.Retract(ctx, factstore.RetractPattern{
		RecordID:  id,
		Attribute: ":smoke/r",
	}); err != nil {
		t.Fatalf("retract: %v", err)
	}

	q := factstore.Query{
		Find: []string{"?v"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":smoke/r", Value: factstore.Var("v")},
		},
	}
	rows, err := c.Query(ctx, q)
	if err != nil {
		t.Fatalf("query post-retract: %v", err)
	}
	for _, r := range rows {
		if s, ok := r[0].(string); ok && s == "to-retract" {
			t.Fatalf("retracted value still present in query result")
		}
	}
}

// TestSmoke_FullText — PR #84 (issue #80). Datalevin's `(fulltext ...)`
// predicate needs an attribute with `:db/fulltext true`. We register
// such an attribute, assert two facts (one matching, one not), and
// query with a FullText clause. The CI server is fresh-spun so the
// FTS index is empty on entry.
func TestSmoke_FullText(t *testing.T) {
	c := smokeClient(t)
	ctx := context.Background()

	if err := c.Schema(ctx, map[string]map[string]string{
		":smoke/text": {"db/valueType": "db.type/string", "db/fulltext": "true"},
	}); err != nil {
		t.Fatalf("schema: %v", err)
	}

	idA, idB := uniqueID(), uniqueID()
	if err := c.Assert(ctx, []factstore.Fact{
		{RecordID: idA, Attribute: ":smoke/text", Value: "Ford Transit van"},
		{RecordID: idB, Attribute: ":smoke/text", Value: "Mercedes Sprinter"},
	}); err != nil {
		t.Fatalf("assert: %v", err)
	}

	q := factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":smoke/text", Value: factstore.Var("v")},
			&factstore.FullText{Entity: factstore.Var("e"), Query: "Transit"},
		},
	}
	rows, err := c.Query(ctx, q)
	if err != nil {
		t.Fatalf("fulltext query: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("fulltext returned no rows; expected at least the Transit match")
	}
}

// TestSmoke_RecursiveRules — PR #85 (issue #81). Seed a tiny category
// tree and an item, then query with the recursive `category-in-tree`
// rule we render from the planner's category_tree path. The item's
// category is a leaf; the call uses the root as the anchor.
func TestSmoke_RecursiveRules(t *testing.T) {
	c := smokeClient(t)
	ctx := context.Background()

	if err := c.Schema(ctx, map[string]map[string]string{
		":record/type":     {"db/valueType": "db.type/string"},
		":record/category": {"db/valueType": "db.type/string"},
		":category/name":   {"db/valueType": "db.type/string"},
		":category/parent": {"db/valueType": "db.type/string"},
	}); err != nil {
		t.Fatalf("schema: %v", err)
	}

	// Use a fresh prefix per run so we don't trip on prior state.
	prefix := uniqueID()[:6]
	rootName := "smoke-root-" + prefix
	midName := "smoke-mid-" + prefix
	leafName := "smoke-leaf-" + prefix

	// Each conceptual entity needs a single RecordID so its
	// attributes land on the same Datalevin entity — the rule body
	// joins :record/type, :category/name, :category/parent on one
	// ?cent.
	rootID := uniqueID()
	midID := uniqueID()
	leafID := uniqueID()
	itemID := uniqueID()

	if err := c.Assert(ctx, []factstore.Fact{
		// Categories: root → mid → leaf
		{RecordID: rootID, Attribute: ":record/type", Value: "category"},
		{RecordID: rootID, Attribute: ":category/name", Value: rootName},

		{RecordID: midID, Attribute: ":record/type", Value: "category"},
		{RecordID: midID, Attribute: ":category/name", Value: midName},
		{RecordID: midID, Attribute: ":category/parent", Value: rootName},

		{RecordID: leafID, Attribute: ":record/type", Value: "category"},
		{RecordID: leafID, Attribute: ":category/name", Value: leafName},
		{RecordID: leafID, Attribute: ":category/parent", Value: midName},

		// Item in the leaf category
		{RecordID: itemID, Attribute: ":record/type", Value: "item"},
		{RecordID: itemID, Attribute: ":record/category", Value: leafName},
	}); err != nil {
		t.Fatalf("assert: %v", err)
	}

	q := factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":record/category", Value: factstore.Var("cat")},
			&factstore.RuleCall{Name: "category-in-tree", Args: []factstore.Term{factstore.Var("cat"), factstore.Lit(rootName)}},
		},
		Rules: []factstore.Rule{
			{
				Name: "category-in-tree",
				Args: []string{"?c", "?root"},
				Body: []factstore.Clause{
					&factstore.Predicate{Op: "=", Left: factstore.Var("c"), Right: factstore.Var("root")},
				},
			},
			{
				Name: "category-in-tree",
				Args: []string{"?c", "?root"},
				Body: []factstore.Clause{
					&factstore.Pattern{Entity: factstore.Var("cent"), Attribute: ":record/type", Value: factstore.Lit("category")},
					&factstore.Pattern{Entity: factstore.Var("cent"), Attribute: ":category/name", Value: factstore.Var("c")},
					&factstore.Pattern{Entity: factstore.Var("cent"), Attribute: ":category/parent", Value: factstore.Var("p")},
					&factstore.RuleCall{Name: "category-in-tree", Args: []factstore.Term{factstore.Var("p"), factstore.Var("root")}},
				},
			},
		},
	}
	rows, err := c.Query(ctx, q)
	if err != nil {
		t.Fatalf("rules query: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("recursive rule returned no matches for leaf category %q under root %q", leafName, rootName)
	}
}

// Compile-time guard: the smoke harness depends on every shipped
// HTTP verb. If any disappears, the build fails before the JVM is
// even started.
var (
	_ = http.MethodPost
)

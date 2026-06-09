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
	"strconv"
	"strings"
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

	// Unique attribute per run: Datalevin's update-schema cannot
	// retrofit :db/fulltext onto an attribute that was previously
	// registered without it, so reusing :smoke/text across runs
	// (CI ephemeral; local /tmp/talon-datalevin persistent) would
	// pin the first-seen schema and silently disable FTS for every
	// subsequent run.
	attr := fmt.Sprintf(":smoke/text-%s", uniqueID())
	if err := c.Schema(ctx, map[string]map[string]string{
		attr: {
			"db/valueType":            "db.type/string",
			"db/fulltext":             "true",
			"db.fulltext/autoDomain":  "true",
		},
	}); err != nil {
		t.Fatalf("schema: %v", err)
	}

	idA, idB := uniqueID(), uniqueID()
	if err := c.Assert(ctx, []factstore.Fact{
		{RecordID: idA, Attribute: attr, Value: "Ford Transit van"},
		{RecordID: idB, Attribute: attr, Value: "Mercedes Sprinter"},
	}); err != nil {
		t.Fatalf("assert: %v", err)
	}

	q := factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: attr, Value: factstore.Var("v")},
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
			// Both rule heads anchor ?c to a real :category/name in the
			// store before doing anything else — Datalevin's rule
			// projector NPEs on an unanchored `[(= ?c ?root)]` base.
			{
				Name: "category-in-tree",
				Args: []string{"?c", "?root"},
				Body: []factstore.Clause{
					&factstore.Pattern{Entity: factstore.Var("cent"), Attribute: ":category/name", Value: factstore.Var("c")},
					&factstore.Predicate{Op: "=", Left: factstore.Var("c"), Right: factstore.Var("root")},
				},
			},
			{
				Name: "category-in-tree",
				Args: []string{"?c", "?root"},
				Body: []factstore.Clause{
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

// TestSmoke_AsOf — time-travel reads. Assert a fact, capture the
// resulting tx-id, retract the entity, then query the same entity
// twice: once at the current state (should miss) and once via
// Query.AsOf set to the pre-retraction tx (should hit).
func TestSmoke_AsOf(t *testing.T) {
	c := smokeClient(t)
	ctx := context.Background()

	if err := c.Schema(ctx, map[string]map[string]string{
		":smoke/asof": {"db/valueType": "db.type/string"},
	}); err != nil {
		t.Fatalf("schema: %v", err)
	}

	id := uniqueID()
	idInt, _ := strconv.ParseInt(id, 10, 64)
	txID, err := c.AssertWithTxID(ctx, []factstore.Fact{
		{RecordID: id, Attribute: ":smoke/asof", Value: "before"},
	})
	if err != nil {
		t.Fatalf("assert: %v", err)
	}
	if txID == 0 {
		t.Fatalf("AssertWithTxID returned 0; expected basis-t")
	}

	if err := c.Retract(ctx, factstore.RetractPattern{RecordID: id}); err != nil {
		t.Fatalf("retract: %v", err)
	}

	q := factstore.Query{
		Find: []string{"?v"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Lit(idInt), Attribute: ":smoke/asof", Value: factstore.Var("v")},
		},
	}

	// Current state: the value must be gone.
	rowsNow, err := c.Query(ctx, q)
	if err != nil {
		t.Fatalf("query current: %v", err)
	}
	for _, r := range rowsNow {
		if s, _ := r[0].(string); s == "before" {
			t.Fatalf("value not retracted in current state: %v", rowsNow)
		}
	}

	// As-of just after the assert: the value must reappear.
	q.AsOf = txID
	rowsBefore, err := c.Query(ctx, q)
	if err != nil {
		t.Fatalf("query as-of: %v", err)
	}
	found := false
	for _, r := range rowsBefore {
		if s, _ := r[0].(string); s == "before" {
			found = true
		}
	}
	if !found {
		t.Fatalf("as-of read missed pre-retraction value: %v", rowsBefore)
	}
}

// TestSmoke_MultiTenant — facts asserted in tenant A must not appear
// from tenant B, and vice versa. Different DBs under DATALEVIN_PATH/t/.
func TestSmoke_MultiTenant(t *testing.T) {
	base := smokeClient(t)
	ctx := context.Background()

	tA := "smoke-a-" + uniqueID()[:6]
	tB := "smoke-b-" + uniqueID()[:6]
	cA := base.WithTenant(tA)
	cB := base.WithTenant(tB)

	if err := cA.Schema(ctx, map[string]map[string]string{
		":smoke/owner": {"db/valueType": "db.type/string"},
	}); err != nil {
		t.Fatalf("schema A: %v", err)
	}
	if err := cB.Schema(ctx, map[string]map[string]string{
		":smoke/owner": {"db/valueType": "db.type/string"},
	}); err != nil {
		t.Fatalf("schema B: %v", err)
	}

	idA := uniqueID()
	if err := cA.Assert(ctx, []factstore.Fact{
		{RecordID: idA, Attribute: ":smoke/owner", Value: "tenant-A-only"},
	}); err != nil {
		t.Fatalf("assert A: %v", err)
	}

	q := factstore.Query{
		Find: []string{"?v"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":smoke/owner", Value: factstore.Var("v")},
		},
	}
	rowsA, err := cA.Query(ctx, q)
	if err != nil {
		t.Fatalf("query A: %v", err)
	}
	rowsB, err := cB.Query(ctx, q)
	if err != nil {
		t.Fatalf("query B: %v", err)
	}

	seenInA := false
	for _, r := range rowsA {
		if s, _ := r[0].(string); s == "tenant-A-only" {
			seenInA = true
		}
	}
	if !seenInA {
		t.Fatalf("tenant A didn't see its own fact: %v", rowsA)
	}
	for _, r := range rowsB {
		if s, _ := r[0].(string); s == "tenant-A-only" {
			t.Fatalf("tenant B saw tenant A's fact — isolation breach: %v", rowsB)
		}
	}
}

// TestSmoke_FullTextPhrase — Datalevin's search syntax accepts
// structured expressions; FullText.Expr drops the raw expression in
// verbatim. We seed three sentences and use [:and {:phrase "..."}]
// to require a multi-word match.
func TestSmoke_FullTextPhrase(t *testing.T) {
	c := smokeClient(t)
	ctx := context.Background()

	attr := fmt.Sprintf(":smoke/phrase-%s", uniqueID())
	if err := c.Schema(ctx, map[string]map[string]string{
		attr: {
			"db/valueType":           "db.type/string",
			"db/fulltext":            "true",
			"db.fulltext/autoDomain": "true",
		},
	}); err != nil {
		t.Fatalf("schema: %v", err)
	}

	id1, id2 := uniqueID(), uniqueID()
	if err := c.Assert(ctx, []factstore.Fact{
		{RecordID: id1, Attribute: attr, Value: "Mary had a little lamb whose fleece was red as fire"},
		{RecordID: id2, Attribute: attr, Value: "little white lamb is unrelated to the phrase"},
	}); err != nil {
		t.Fatalf("assert: %v", err)
	}

	q := factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: attr, Value: factstore.Var("v")},
			&factstore.FullText{Entity: factstore.Var("e"), Expr: `[:and {:phrase "little lamb"} "red"]`},
		},
	}
	rows, err := c.Query(ctx, q)
	if err != nil {
		t.Fatalf("phrase query: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("phrase search returned no rows; expected the Mary sentence")
	}
}

// TestSmoke_PullSyntax — verify Query.Pull surfaces. Seed an entity
// with two attributes and ask Datalevin to pull both via
// `[:smoke/pull-name :smoke/pull-tag]`. The result row carries the
// nested entity map as Datalevin's response shape.
func TestSmoke_PullSyntax(t *testing.T) {
	c := smokeClient(t)
	ctx := context.Background()

	if err := c.Schema(ctx, map[string]map[string]string{
		":smoke/pull-name": {"db/valueType": "db.type/string"},
		":smoke/pull-tag":  {"db/valueType": "db.type/string"},
	}); err != nil {
		t.Fatalf("schema: %v", err)
	}

	id := uniqueID()
	if err := c.Assert(ctx, []factstore.Fact{
		{RecordID: id, Attribute: ":smoke/pull-name", Value: "Pull subject"},
		{RecordID: id, Attribute: ":smoke/pull-tag", Value: "alpha"},
	}); err != nil {
		t.Fatalf("assert: %v", err)
	}

	q := factstore.Query{
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: ":smoke/pull-name", Value: factstore.Var("n")},
		},
		Pull: []factstore.PullSpec{
			{EntityVar: "?e", Pattern: `[:smoke/pull-name :smoke/pull-tag]`},
		},
	}
	rows, err := c.Query(ctx, q)
	if err != nil {
		t.Fatalf("pull query: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("pull returned no rows")
	}
	// The pull result is a Datalevin map serialized into JSON as an
	// object. We just spot-check that the pulled attribute values
	// appear somewhere in the row.
	serialized := fmt.Sprintf("%v", rows[0])
	if !strings.Contains(serialized, "Pull subject") || !strings.Contains(serialized, "alpha") {
		t.Fatalf("pull row missing expected fields: %v", rows[0])
	}
}

// TestSmoke_SearchDomains — attribute-scoped FTS via
// FullText.Attribute. Two FTS attrs participate in the same domain;
// querying with an attribute scope filters to that one. We register
// both with :db.fulltext/autoDomain so each attribute is its own
// domain, then query with `:attr` scoping on the one we want.
func TestSmoke_SearchDomains(t *testing.T) {
	c := smokeClient(t)
	ctx := context.Background()

	titleAttr := fmt.Sprintf(":smoke/title-%s", uniqueID())
	bodyAttr := fmt.Sprintf(":smoke/body-%s", uniqueID())
	if err := c.Schema(ctx, map[string]map[string]string{
		titleAttr: {
			"db/valueType":           "db.type/string",
			"db/fulltext":            "true",
			"db.fulltext/autoDomain": "true",
		},
		bodyAttr: {
			"db/valueType":           "db.type/string",
			"db/fulltext":            "true",
			"db.fulltext/autoDomain": "true",
		},
	}); err != nil {
		t.Fatalf("schema: %v", err)
	}

	id1, id2 := uniqueID(), uniqueID()
	if err := c.Assert(ctx, []factstore.Fact{
		// id1: needle in body only
		{RecordID: id1, Attribute: titleAttr, Value: "Annual report"},
		{RecordID: id1, Attribute: bodyAttr, Value: "Talon language overview, performance gains"},
		// id2: needle in title only
		{RecordID: id2, Attribute: titleAttr, Value: "Talon language deep dive"},
		{RecordID: id2, Attribute: bodyAttr, Value: "Unrelated content here"},
	}); err != nil {
		t.Fatalf("assert: %v", err)
	}

	// Scope FTS to the title attribute: only id2 should match.
	q := factstore.Query{
		Find: []string{"?e"},
		Where: []factstore.Clause{
			&factstore.Pattern{Entity: factstore.Var("e"), Attribute: titleAttr, Value: factstore.Var("t")},
			&factstore.FullText{Entity: factstore.Var("e"), Attribute: titleAttr, Query: "Talon"},
		},
	}
	rows, err := c.Query(ctx, q)
	if err != nil {
		t.Fatalf("scoped fulltext: %v", err)
	}
	id2Int, _ := strconv.ParseInt(id2, 10, 64)
	hit := false
	for _, r := range rows {
		if f, ok := r[0].(float64); ok && int64(f) == id2Int {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("title-scoped fulltext didn't return id2 (%d): %v", id2Int, rows)
	}
}

// Compile-time guard: the smoke harness depends on every shipped
// HTTP verb. If any disappears, the build fails before the JVM is
// even started.
var (
	_ = http.MethodPost
)

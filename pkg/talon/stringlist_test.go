package talon_test

import (
	"context"
	"testing"

	"github.com/opentalon/talon-language/internal/factstore"
	"github.com/opentalon/talon-language/pkg/talon"
)

const listContainsSrc = `
detect "List contains" {
  for records where type == "pr"
    and attr "changed_files" contains "go.mod"
  flag matching items
  label "touches go.mod"
  priority HIGH
}`

const listMatchesSrc = `
detect "List matches" {
  for records where type == "pr"
    and attr "changed_files" matches "main.go"
  flag matching items
  label "touches main.go"
  priority HIGH
}`

const listMatchesPhraseSrc = `
detect "List matches phrase" {
  for records where type == "pr"
    and attr "changed_files" matches phrase "main.go"
  flag matching items
  label "touches main.go"
  priority HIGH
}`

func seedListPRs(t *testing.T) *factstore.MemoryStore {
	t.Helper()
	store := talon.NewMemoryStore()
	facts := []talon.Fact{
		{RecordID: "1", Attribute: ":record/type", Value: "pr"},
		{RecordID: "1", Attribute: ":attr/changed_files", Value: []any{"go.mod", "main.go"}},
		{RecordID: "2", Attribute: ":record/type", Value: "pr"},
		{RecordID: "2", Attribute: ":attr/changed_files", Value: "go.mod,main.go"},
		{RecordID: "3", Attribute: ":record/type", Value: "pr"},
		{RecordID: "3", Attribute: ":attr/changed_files", Value: []any{"README.md"}},
	}
	if err := store.Assert(context.Background(), facts); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return store
}

// Issue #158: `contains` against a list-valued attribute used to evaluate to
// false, so only the joined-string entity matched. Both shapes must match.
func TestRun_StringPredicateQuantifiesOverList(t *testing.T) {
	store := seedListPRs(t)
	res, err := talon.Run(context.Background(), listContainsSrc, talon.WithFactStore(store))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	block := res.Blocks["List contains"]
	if block == nil {
		t.Fatalf("no result for detect block, got %v", res.Blocks)
	}
	got := map[float64]bool{}
	for _, row := range block.Flagged {
		id, ok := row[0].(float64)
		if !ok {
			t.Fatalf("row %v: entity id is %T, want float64", row, row[0])
		}
		got[id] = true
	}
	if len(got) != 2 || !got[1] || !got[2] {
		t.Errorf("flagged %v, want entities 1 and 2", got)
	}
}

// `matches` scans list elements, and `matches_phrase` — which lowered to a
// Datalevin-only search expression and so matched nothing on MemoryStore —
// now carries plain-text fallback the local backends can evaluate.
func TestRun_FullTextOverList(t *testing.T) {
	for _, tc := range []struct {
		name  string
		src   string
		block string
	}{
		{"matches", listMatchesSrc, "List matches"},
		{"matches_phrase", listMatchesPhraseSrc, "List matches phrase"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := seedListPRs(t)
			res, err := talon.Run(context.Background(), tc.src, talon.WithFactStore(store))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			block := res.Blocks[tc.block]
			if block == nil {
				t.Fatalf("no result for %q, got %v", tc.block, res.Blocks)
			}
			got := map[float64]bool{}
			for _, row := range block.Flagged {
				got[row[0].(float64)] = true
			}
			if len(got) != 2 || !got[1] || !got[2] {
				t.Errorf("flagged %v, want entities 1 and 2", got)
			}
		})
	}
}

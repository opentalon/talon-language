package talon_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/opentalon/talon-language/pkg/talon"
)

const actionSrc = `
rule "Tenant approve" {
  for records where type == "pr" and attr "risk" == "low"
  do approve "pr" attr "owner"
  do comment "pr" "{attr.owner} touched {attr.files} files"
}
`

const actionSeed = `
test "seed" {
  given {
    record 7 type "pr"
    attr 7 "risk" "low"
    attr 7 "owner" "@alice"
    attr 7 "files" 3
  }
  when rule "Tenant approve"
  expect { flagged 7 }
}
`

func runWithActions(t *testing.T, src, seed string) *talon.Result {
	t.Helper()
	store := talon.NewMemoryStore()
	if _, err := talon.Seed(context.Background(), store, seed); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	res, err := talon.Run(context.Background(), src, talon.WithFactStore(store))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

// The whole point of the public surface: a host embedding the engine gets
// the fired actions back, resolved, without running the test runner.
func TestRun_ExposesFiredActions(t *testing.T) {
	res := runWithActions(t, actionSrc, actionSeed)
	want := []talon.Action{
		{EntityID: 7, Rule: "Tenant approve", Verb: "approve", Args: []any{"pr", "@alice"}},
		{EntityID: 7, Rule: "Tenant approve", Verb: "comment", Args: []any{"pr", "@alice touched 3 files"}},
	}
	if !reflect.DeepEqual(res.Actions, want) {
		t.Fatalf("Actions:\n got %#v\nwant %#v", res.Actions, want)
	}
	if !reflect.DeepEqual(res.Blocks["Tenant approve"].Actions, want) {
		t.Fatalf("block actions differ from run actions: %#v", res.Blocks["Tenant approve"].Actions)
	}
}

// A program with no rules at all still reports an empty list, so a caller
// can range over it without a nil check and can't confuse "no actions"
// with "actions not implemented".
func TestRun_ActionsEmptyNotNilWithoutRules(t *testing.T) {
	src := `
detect "Low stock" {
  for records where type == "pr"
  flag matching items
  label "{item.name}"
}`
	res := runWithActions(t, src, actionSeed)
	if res.Actions == nil {
		t.Fatal("Result.Actions is nil; want empty slice")
	}
	if len(res.Actions) != 0 {
		t.Fatalf("want no actions, got %#v", res.Actions)
	}
}

// Determinism across runs is what the downstream GitHub Action's replay
// guarantee rests on: same facts, same ruleset, same bytes.
func TestRun_ActionsDeterministic(t *testing.T) {
	src := actionSrc + `
rule "Label size" {
  for records where type == "pr" and attr "files" > 1
  do label "pr" "size/small"
}
`
	first := runWithActions(t, src, actionSeed).Actions
	if len(first) != 3 {
		t.Fatalf("want 3 actions, got %#v", first)
	}
	for i := 0; i < 5; i++ {
		got := runWithActions(t, src, actionSeed).Actions
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d differs:\n got %#v\nwant %#v", i, got, first)
		}
	}
}

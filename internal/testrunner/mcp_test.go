package testrunner

import (
	"strings"
	"testing"
)

const mcpRules = `
detect "Defective without ticket" {
  for records where status == "defective"
  flag matching items
  remediate {
    mcp "inventory" "create-ticket" {
      item_id attr "id"
      title "Auto: {item.name}"
      priority "high"
    }
  }
}
`

func TestMCPCalledPasses(t *testing.T) {
	res := runResults(t, mcpRules, `
test "creates ticket" {
  given {
    record 501 type "item" status "defective"
    attr 501 "name" "Broken Drill"
  }
  mock mcp "inventory" "create-ticket" { returns { id 801  status "open" } }
  when detect "Defective without ticket"
  expect {
    flagged 501
    mcp_called "inventory" "create-ticket" with {
      item_id == 501
      title contains "Broken Drill"
      priority == "high"
    }
  }
}`)
	if len(res) != 1 || !res[0].Passed {
		t.Fatalf("expected pass, got %+v", res)
	}
}

func TestMCPCalledFailsOnWrongArg(t *testing.T) {
	res := runResults(t, mcpRules, `
test "wrong arg" {
  given {
    record 501 type "item" status "defective"
    attr 501 "name" "Broken Drill"
  }
  mock mcp "inventory" "create-ticket" { returns { id 801 } }
  when detect "Defective without ticket"
  expect {
    mcp_called "inventory" "create-ticket" with { item_id == 999 }
  }
}`)
	if len(res) != 1 || res[0].Passed {
		t.Fatalf("expected failure, got %+v", res)
	}
	if !strings.Contains(strings.Join(res[0].Errors, " "), "no call matched") {
		t.Errorf("expected 'no call matched' error, got %v", res[0].Errors)
	}
}

func TestMCPCalledFailsWhenNotCalled(t *testing.T) {
	res := runResults(t, mcpRules, `
test "not defective" {
  given {
    record 501 type "item" status "active"
    attr 501 "name" "Fine Drill"
  }
  when detect "Defective without ticket"
  expect {
    mcp_called "inventory" "create-ticket"
  }
}`)
	if len(res) != 1 || res[0].Passed {
		t.Fatalf("expected failure (never called), got %+v", res)
	}
	if !strings.Contains(strings.Join(res[0].Errors, " "), "not called") {
		t.Errorf("expected 'not called' error, got %v", res[0].Errors)
	}
}

func TestMCPMockFailsWithOnErrorSkip(t *testing.T) {
	rules := `
detect "Defective without ticket" {
  for records where status == "defective"
  flag matching items
  remediate {
    mcp "inventory" "create-ticket" {
      item_id attr "id"
      on_error { log "failed: {error}"  then skip }
    }
  }
}`
	res := runResults(t, rules, `
test "mock failure is swallowed by on_error skip" {
  given {
    record 501 type "item" status "defective"
  }
  mock mcp "inventory" "create-ticket" { fails "boom" }
  when detect "Defective without ticket"
  expect {
    flagged 501
    mcp_called "inventory" "create-ticket"
  }
}`)
	// The call was attempted (and recorded) then skipped; the block still
	// completes, so the test passes.
	if len(res) != 1 || !res[0].Passed {
		t.Fatalf("expected pass (call recorded, failure skipped), got %+v", res)
	}
}

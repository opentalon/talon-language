package testrunner

import "testing"

// TestRunWasAgoWiring exercises the `was ... ago` plan end-to-end through
// the testrunner. Fixtures carry a single current state (asserted at the
// epoch), so the time-travel query sees the fixture value: a machine
// currently certified matches `was (status == certified) ...`, a defective
// one does not. Proves the plan split + intersect narrow correctly.
func TestRunWasAgoWiring(t *testing.T) {
	rulesSrc := `
detect "Formerly certified" {
  for records where type == "machine"
    and was (status == "certified") 90 days ago
  flag matching items
  label "x"
  priority HIGH
}`
	testSrc := `
test "was certified" {
  given {
    record 1 type "machine" status "certified"
    record 2 type "machine" status "defective"
  }
  when detect "Formerly certified"
  expect {
    flagged 1
    not flagged 2
  }
}`
	results := runResults(t, rulesSrc, testSrc)
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if !results[0].Passed {
		t.Fatalf("assertions failed: %v", results[0].Errors)
	}
}

package testrunner

import "testing"

// classifyRules: a supervised classify block. Candidates are open incidents;
// the vote is drawn from resolved incidents whose root_cause is known. Two
// well-separated failure signatures (high vibration → "bearing", high temp →
// "overheat"); the confidence bound drops any candidate that lands between
// them.
const classifyRules = `
classify "Failure mode" {
  for records where type == "incident" and status == "open"
  features [attr "vibration", attr "temp"]
  trained_on records where type == "incident" and status == "resolved"
  label_attr "root_cause"
  confidence >= 0.8
  label "likely cause: {class}"
}`

// tenLabeledIncidents: 5 bearing (high vibration / low temp) + 5 overheat
// (low vibration / high temp), all resolved so they carry root_cause.
const tenLabeledIncidents = `
    record 1 type "incident" status "resolved" vibration 90 temp 20 root_cause "bearing"
    record 2 type "incident" status "resolved" vibration 92 temp 22 root_cause "bearing"
    record 3 type "incident" status "resolved" vibration 88 temp 18 root_cause "bearing"
    record 4 type "incident" status "resolved" vibration 91 temp 19 root_cause "bearing"
    record 5 type "incident" status "resolved" vibration 89 temp 21 root_cause "bearing"
    record 6 type "incident" status "resolved" vibration 20 temp 90 root_cause "overheat"
    record 7 type "incident" status "resolved" vibration 22 temp 92 root_cause "overheat"
    record 8 type "incident" status "resolved" vibration 18 temp 88 root_cause "overheat"
    record 9 type "incident" status "resolved" vibration 19 temp 91 root_cause "overheat"
    record 10 type "incident" status "resolved" vibration 21 temp 89 root_cause "overheat"`

// TestClassifyFlagsConfidentPredictions: two open incidents sit squarely in a
// cluster (confident → flagged); one sits between the clusters (split vote,
// confidence below 0.8 → dropped).
func TestClassifyFlagsConfidentPredictions(t *testing.T) {
	tests := `
test "classify open incidents" {
  given {` + tenLabeledIncidents + `
    record 100 type "incident" status "open" vibration 91 temp 21
    record 101 type "incident" status "open" vibration 20 temp 90
    record 102 type "incident" status "open" vibration 55 temp 55
  }
  when classify "Failure mode"
  expect {
    flagged 100
    flagged 101
    not flagged 102
  }
}`
	res := runResults(t, classifyRules, tests)
	if len(res) != 1 {
		t.Fatalf("want 1 test result, got %d", len(res))
	}
	if !res[0].Passed {
		t.Fatalf("classify test failed: %v", res[0].Errors)
	}
}

// TestClassifyWithoutConfidenceKeepsAll: drop the confidence bound and every
// candidate is labeled and kept — classify is informational, not a filter,
// unless a bound is set.
func TestClassifyWithoutConfidenceKeepsAll(t *testing.T) {
	rules := `
classify "Failure mode" {
  for records where type == "incident" and status == "open"
  features [attr "vibration", attr "temp"]
  trained_on records where type == "incident" and status == "resolved"
  label_attr "root_cause"
}`
	tests := `
test "no bound keeps borderline" {
  given {` + tenLabeledIncidents + `
    record 102 type "incident" status "open" vibration 55 temp 55
  }
  when classify "Failure mode"
  expect {
    flagged 102
  }
}`
	res := runResults(t, rules, tests)
	if len(res) != 1 || !res[0].Passed {
		t.Fatalf("classify (no bound) failed: %v", res[0].Errors)
	}
}

package testrunner

import "testing"

// predictRules: a decision-tree predict block. Candidates are in-service
// machines; the tree trains on retired machines whose real outcome
// ("failed" / "ok") is known, splitting on operating hours and repair count.
// The confidence bound drops predictions from an impure leaf.
const predictRules = `
predict "Failure risk" {
  for records where type == "machine" and status == "in_service"
  features [attr "operating_hours", attr "repair_count"]
  trained_on records where type == "machine" and status == "retired"
  label_attr "outcome"
  confidence >= 0.9
  label "risk: {class}"
  priority HIGH
}`

// retiredMachines: a clean rule — machines with many operating hours AND
// several repairs failed; everything else was fine. A single-threshold split
// on operating_hours separates the bulk, with repair_count refining it.
const retiredMachines = `
    record 1 type "machine" status "retired" operating_hours 3000 repair_count 5 outcome "failed"
    record 2 type "machine" status "retired" operating_hours 3200 repair_count 6 outcome "failed"
    record 3 type "machine" status "retired" operating_hours 2800 repair_count 4 outcome "failed"
    record 4 type "machine" status "retired" operating_hours 3100 repair_count 7 outcome "failed"
    record 5 type "machine" status "retired" operating_hours 2900 repair_count 5 outcome "failed"
    record 6 type "machine" status "retired" operating_hours 500 repair_count 0 outcome "ok"
    record 7 type "machine" status "retired" operating_hours 400 repair_count 1 outcome "ok"
    record 8 type "machine" status "retired" operating_hours 600 repair_count 0 outcome "ok"
    record 9 type "machine" status "retired" operating_hours 300 repair_count 1 outcome "ok"
    record 10 type "machine" status "retired" operating_hours 550 repair_count 0 outcome "ok"`

// TestPredictFlagsHighRisk: an in-service machine deep in the failed region is
// predicted "failed" from a pure leaf (kept); one deep in the healthy region
// is "ok" from a pure leaf — also a confident prediction, but of the "ok"
// class. Both clear the 0.9 bound, so both are flagged (predict labels; it
// doesn't drop the negative class).
func TestPredictClassifiesByTree(t *testing.T) {
	tests := `
test "predict failure risk" {
  given {` + retiredMachines + `
    record 100 type "machine" status "in_service" operating_hours 3100 repair_count 6
    record 101 type "machine" status "in_service" operating_hours 450 repair_count 0
  }
  when predict "Failure risk"
  expect {
    flagged 100
    flagged 101
  }
}`
	res := runResults(t, predictRules, tests)
	if len(res) != 1 {
		t.Fatalf("want 1 test result, got %d", len(res))
	}
	if !res[0].Passed {
		t.Fatalf("predict test failed: %v", res[0].Errors)
	}
}

// TestPredictConfidenceDropsImpureLeaf: with contradictory training at the
// same feature point, the leaf can't be pure, so its purity falls below the
// 0.9 bound and the matching candidate is dropped.
func TestPredictConfidenceDropsImpureLeaf(t *testing.T) {
	rules := `
predict "Failure risk" {
  for records where type == "machine" and status == "in_service"
  features [attr "operating_hours"]
  trained_on records where type == "machine" and status == "retired"
  label_attr "outcome"
  confidence >= 0.9
}`
	// All training rows share one operating_hours value with a near-even
	// label split → a single impure leaf (~0.55 purity), below 0.9.
	tests := `
test "impure leaf dropped" {
  given {
    record 1 type "machine" status "retired" operating_hours 1000 outcome "failed"
    record 2 type "machine" status "retired" operating_hours 1000 outcome "failed"
    record 3 type "machine" status "retired" operating_hours 1000 outcome "failed"
    record 4 type "machine" status "retired" operating_hours 1000 outcome "failed"
    record 5 type "machine" status "retired" operating_hours 1000 outcome "failed"
    record 6 type "machine" status "retired" operating_hours 1000 outcome "ok"
    record 7 type "machine" status "retired" operating_hours 1000 outcome "ok"
    record 8 type "machine" status "retired" operating_hours 1000 outcome "ok"
    record 9 type "machine" status "retired" operating_hours 1000 outcome "ok"
    record 100 type "machine" status "in_service" operating_hours 1000
  }
  when predict "Failure risk"
  expect {
    not flagged 100
  }
}`
	res := runResults(t, rules, tests)
	if len(res) != 1 || !res[0].Passed {
		t.Fatalf("predict (impure leaf) failed: %v", res[0].Errors)
	}
}

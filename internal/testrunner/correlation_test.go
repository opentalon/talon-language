package testrunner

import "testing"

const correlationRule = `
detect "Mileage vs failures" {
  for records where type == "vehicle"
    and attr "km" correlates_with attr "failure_count" over last 90 days > 0.7
  flag matching items
  label "correlated"
  priority MEDIUM
}`

// TestRunCorrelationFlagsWhenCorrelated: km and failure_count move together
// (failure = km/10) across the fleet → r = 1.0 > 0.7, so every vehicle is
// flagged (the condition gates the whole population).
func TestRunCorrelationFlagsWhenCorrelated(t *testing.T) {
	testSrc := `
test "correlated fleet" {
  given {
    record 1 type "vehicle"
    attr 1 "km" 10
    attr 1 "failure_count" 1
    record 2 type "vehicle"
    attr 2 "km" 20
    attr 2 "failure_count" 2
    record 3 type "vehicle"
    attr 3 "km" 30
    attr 3 "failure_count" 3
    record 4 type "vehicle"
    attr 4 "km" 40
    attr 4 "failure_count" 4
    record 5 type "vehicle"
    attr 5 "km" 50
    attr 5 "failure_count" 5
  }
  when detect "Mileage vs failures"
  expect {
    flagged 1
    flagged 3
    flagged 5
  }
}`
	results := runResults(t, correlationRule, testSrc)
	if len(results) != 1 || !results[0].Passed {
		t.Fatalf("assertions failed: %+v", results[0].Errors)
	}
}

// TestRunCorrelationDropsWhenUncorrelated: failure_count is unrelated to km
// (r well below 0.7) → no vehicle is flagged.
func TestRunCorrelationDropsWhenUncorrelated(t *testing.T) {
	testSrc := `
test "uncorrelated fleet" {
  given {
    record 1 type "vehicle"
    attr 1 "km" 10
    attr 1 "failure_count" 5
    record 2 type "vehicle"
    attr 2 "km" 20
    attr 2 "failure_count" 1
    record 3 type "vehicle"
    attr 3 "km" 30
    attr 3 "failure_count" 9
    record 4 type "vehicle"
    attr 4 "km" 40
    attr 4 "failure_count" 2
    record 5 type "vehicle"
    attr 5 "km" 50
    attr 5 "failure_count" 6
  }
  when detect "Mileage vs failures"
  expect {
    not flagged 1
    not flagged 2
    not flagged 3
    not flagged 4
    not flagged 5
  }
}`
	results := runResults(t, correlationRule, testSrc)
	if len(results) != 1 || !results[0].Passed {
		t.Fatalf("assertions failed: %+v", results[0].Errors)
	}
}

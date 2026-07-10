package testrunner

import "testing"

// A cached threshold feeds an arithmetic selector: an item is overdue when its
// odometer has advanced more than the cached service interval past its last
// service. The value (18200) must actually be used in the comparison.
const thresholdRules = `
threshold "service_interval" {
  value 18200
  computed_from "47 service tickets, avg 20222 km, margin 0.9"
  valid_until "2099-05-13"
}

detect "Service overdue" {
  for records where type == "item"
    and attr "km" > attr "last_service_km" + threshold "service_interval"
  flag matching items
  priority HIGH
}`

func TestCachedThresholdGatesArithmetic(t *testing.T) {
	tests := `
test "cached threshold gates" {
  given {
    record 1 type "item"
    attr 1 "km" 45000
    attr 1 "last_service_km" 25000
    record 2 type "item"
    attr 2 "km" 40000
    attr 2 "last_service_km" 25000
  }
  when detect "Service overdue"
  expect {
    flagged 1       // 45000 > 25000 + 18200 (=43200)
    not flagged 2   // 40000 < 43200
  }
}`
	res := runResults(t, thresholdRules, tests)
	if len(res) != 1 {
		t.Fatalf("want 1 test result, got %d", len(res))
	}
	if !res[0].Passed {
		t.Fatalf("cached-threshold test failed: %v", res[0].Errors)
	}
}

package testrunner

import "testing"

const calcRule = `
detect "Overheating fleet" {
  for records where type == "sensor"
  calculate avg_temp from records where type == "reading" of attr "temp" average
  having avg_temp > 50
  flag matching items
  label "avg temp {avg_temp}"
  priority HIGH
}`

// TestRunCalculateAvgHavingFlags: the population-average of the reading temps
// clears the `having` bar, so every sensor is flagged. Proves calculate
// reduces to a real scalar, binds it, and `having` consumes it.
func TestRunCalculateAvgHavingFlags(t *testing.T) {
	testSrc := `
test "hot" {
  given {
    record 1 type "sensor"
    record 2 type "sensor"
    record 10 type "reading"
    attr 10 "temp" 40
    record 11 type "reading"
    attr 11 "temp" 80
  }
  when detect "Overheating fleet"
  expect { flagged 1  flagged 2 }
}`
	res := runResults(t, calcRule, testSrc)
	if len(res) != 1 || !res[0].Passed {
		t.Fatalf("assertions failed: %+v", res[0].Errors)
	}
}

// TestRunCalculateAvgHavingDrops: a low population-average closes the gate.
func TestRunCalculateAvgHavingDrops(t *testing.T) {
	testSrc := `
test "cool" {
  given {
    record 1 type "sensor"
    record 10 type "reading"
    attr 10 "temp" 10
    record 11 type "reading"
    attr 11 "temp" 20
  }
  when detect "Overheating fleet"
  expect { not flagged 1 }
}`
	res := runResults(t, calcRule, testSrc)
	if len(res) != 1 || !res[0].Passed {
		t.Fatalf("assertions failed: %+v", res[0].Errors)
	}
}

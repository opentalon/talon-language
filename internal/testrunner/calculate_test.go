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

const wmaRule = `
detect "Rising usage" {
  for records where type == "depot"
  calculate rate from records where type == "reading" of attr "amount" weighted_moving_average
  having rate > 30
  flag matching items
  label "recent rate {rate}"
  priority HIGH
}`

// TestRunWMARecencyGatesGate: the reading series has flat mean 30, but wma
// weights recent (higher-id) records more. A rising series (…,60 newest)
// lifts the wma above 30 → flagged; a falling series (60,…) drops it → not.
func TestRunWMARecencyOpensGate(t *testing.T) {
	rising := `
test "rising" {
  given {
    record 1 type "depot"
    record 10 type "reading"
    attr 10 "amount" 10
    record 11 type "reading"
    attr 11 "amount" 20
    record 12 type "reading"
    attr 12 "amount" 60
  }
  when detect "Rising usage"
  expect { flagged 1 }
}`
	if res := runResults(t, wmaRule, rising); len(res) != 1 || !res[0].Passed {
		t.Fatalf("rising: %+v", res[0].Errors)
	}
}

func TestRunWMARecencyClosesGate(t *testing.T) {
	falling := `
test "falling" {
  given {
    record 1 type "depot"
    record 10 type "reading"
    attr 10 "amount" 60
    record 11 type "reading"
    attr 11 "amount" 20
    record 12 type "reading"
    attr 12 "amount" 10
  }
  when detect "Rising usage"
  expect { not flagged 1 }
}`
	if res := runResults(t, wmaRule, falling); len(res) != 1 || !res[0].Passed {
		t.Fatalf("falling: %+v", res[0].Errors)
	}
}

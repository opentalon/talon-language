package testrunner

import "testing"

// The acceptance chain from issue #91: a `derive` predicate feeds a `detect`,
// which a `recommend` consumes — no host glue. `overdue(v)` is a derived
// predicate (with arithmetic); the detect references it and adds its own
// filter; the recommend fires off the detect.
const deriveRules = `
derive overdue(v) {
  for records where type == "vehicle"
    and attr "km" > attr "last_service_km" + 20000
}

detect "Recall candidates" {
  for records where overdue(v) and attr "model" in ["Transit", "Sprinter"]
  flag matching items
  priority HIGH
}

recommend "Book recall service" {
  when detect "Recall candidates" matches
  suggest "book {item.name} in for a recall service"
}`

func TestDeriveFeedsDetect(t *testing.T) {
	tests := `
test "derived overdue predicate gates the detect" {
  given {
    // overdue (75000 > 50000+20000) AND a recall model → flagged
    record 1 type "vehicle"
    attr 1 "model" "Transit"
    attr 1 "km" 75000
    attr 1 "last_service_km" 50000
    // overdue but not a recall model → not flagged
    record 2 type "vehicle"
    attr 2 "model" "Civic"
    attr 2 "km" 75000
    attr 2 "last_service_km" 50000
    // recall model but not overdue (60000 < 50000+20000) → not flagged
    record 3 type "vehicle"
    attr 3 "model" "Sprinter"
    attr 3 "km" 60000
    attr 3 "last_service_km" 50000
  }
  when detect "Recall candidates"
  expect {
    flagged 1
    not flagged 2
    not flagged 3
  }
}`
	res := runResults(t, deriveRules, tests)
	if len(res) != 1 {
		t.Fatalf("want 1 test result, got %d", len(res))
	}
	if !res[0].Passed {
		t.Fatalf("derive→detect test failed: %v", res[0].Errors)
	}
}

// TestDeriveNegationArithmetic pins the stratified-negation contract for a
// derived predicate whose body needs a Go-side arithmetic filter. The whole
// negation must be evaluated per-row (with `overdue` inlined) — the earlier
// split that pushed the Datalog part into the store's `Not` and leaked the
// arithmetic out as a *positive* filter flipped the sign and flagged exactly
// the wrong vehicles.
func TestDeriveNegationArithmetic(t *testing.T) {
	rules := `
derive overdue(v) {
  for records where type == "vehicle"
    and attr "km" > attr "last_service_km" + 20000
}

detect "Up to date" {
  for records where type == "vehicle" and status == "active" and not overdue(v)
  flag matching items
}`
	tests := `
test "not overdue flags only up-to-date vehicles" {
  given {
    // overdue (80000 > 55000+20000) → excluded by the negation
    record 1 type "vehicle" status "active"
    attr 1 "km" 80000
    attr 1 "last_service_km" 55000
    // not overdue (30000 < 20000+20000) → flagged
    record 2 type "vehicle" status "active"
    attr 2 "km" 30000
    attr 2 "last_service_km" 20000
    // not overdue but inactive → dropped by the sibling filter
    record 3 type "vehicle" status "retired"
    attr 3 "km" 10000
    attr 3 "last_service_km" 5000
  }
  when detect "Up to date"
  expect {
    flagged 2
    not flagged 1
    not flagged 3
  }
}`
	res := runResults(t, rules, tests)
	if len(res) != 1 {
		t.Fatalf("want 1 test result, got %d", len(res))
	}
	if !res[0].Passed {
		t.Fatalf("negation-with-arithmetic test failed: %v", res[0].Errors)
	}
}

// TestDeriveChainOfDerives: a derive that references another derive inlines
// transitively.
func TestDeriveChainOfDerives(t *testing.T) {
	rules := `
derive overdue(v) {
  for records where type == "vehicle" and attr "km" > attr "last_service_km" + 20000
}

derive due_for_recall(v) {
  for records where overdue(v) and attr "model" in ["Transit", "Sprinter"]
}

detect "Recall" {
  for records where due_for_recall(v)
  flag matching items
}`
	tests := `
test "transitive derive" {
  given {
    record 1 type "vehicle"
    attr 1 "model" "Transit"
    attr 1 "km" 75000
    attr 1 "last_service_km" 50000
    record 2 type "vehicle"
    attr 2 "model" "Civic"
    attr 2 "km" 75000
    attr 2 "last_service_km" 50000
  }
  when detect "Recall"
  expect {
    flagged 1
    not flagged 2
  }
}`
	res := runResults(t, rules, tests)
	if len(res) != 1 || !res[0].Passed {
		t.Fatalf("transitive derive test failed: %v", res[0].Errors)
	}
}

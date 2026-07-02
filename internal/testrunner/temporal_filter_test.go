package testrunner

import (
	"testing"
	"time"
)

// withFixedNow pins the Filter-step clock for deterministic date logic.
func withFixedNow(t *testing.T, now time.Time) {
	t.Helper()
	prev := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = prev })
}

func TestApproachingFiltersEndToEnd(t *testing.T) {
	withFixedNow(t, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	rules := `
detect "Service due soon" {
  for records where type == "vehicle" and attr "next_service_date" approaching within 7 days
  flag matching items
}`
	res := runResults(t, rules, `
test "only within-7-days vehicles flagged" {
  given {
    record 1 type "vehicle"
    attr 1 "next_service_date" "2026-07-04"
    record 2 type "vehicle"
    attr 2 "next_service_date" "2026-08-15"
  }
  when detect "Service due soon"
  expect {
    flagged 1
    not flagged 2
  }
}`)
	if len(res) != 1 || !res[0].Passed {
		t.Fatalf("approaching should filter: %+v", res)
	}
}

func TestOlderThanFiltersEndToEnd(t *testing.T) {
	withFixedNow(t, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	rules := `
detect "Stale certs" {
  for records where type == "cert" and attr "issued" older_than 90 days
  flag matching items
}`
	res := runResults(t, rules, `
test "only stale certs flagged" {
  given {
    record 1 type "cert"
    attr 1 "issued" "2020-01-01"
    record 2 type "cert"
    attr 2 "issued" "2026-06-20"
  }
  when detect "Stale certs"
  expect {
    flagged 1
    not flagged 2
  }
}`)
	if len(res) != 1 || !res[0].Passed {
		t.Fatalf("older_than should filter: %+v", res)
	}
}

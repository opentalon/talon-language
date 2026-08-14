package validator

import (
	"strings"
	"testing"

	"github.com/opentalon/tln-language/internal/diagnostic"
)

func TestValidateThresholdClean(t *testing.T) {
	mustClean(t, `
threshold "service_interval" {
  value 18200
  computed_from "47 tickets"
  valid_until "2099-05-13"
}

detect "Service overdue" {
  for records where type == "item"
    and attr "km" > attr "last_service_km" + threshold "service_interval"
  flag matching items
  priority HIGH
}`)
}

func TestValidateUndeclaredThresholdRef(t *testing.T) {
	mustError(t, `
detect "Service overdue" {
  for records where type == "item"
    and attr "km" > threshold "missing_one"
  flag matching items
}`, `undeclared threshold "missing_one"`)
}

func TestValidateThresholdBadDate(t *testing.T) {
	mustError(t, `
threshold "x" {
  value 5
  valid_until "not-a-date"
}`, "is not a date")
}

// TestValidateThresholdExpired: a past valid_until is a warning, not an error —
// the stale value is still usable, and the host is expected to refresh it.
func TestValidateThresholdExpired(t *testing.T) {
	diags := pipeline(t, `
threshold "old" {
  value 5
  valid_until "2000-01-01"
}

detect "D" {
  for records where type == "item" and attr "km" > threshold "old"
  flag matching items
}`)
	if diags.HasErrors() {
		t.Fatalf("expired threshold should warn, not error: %v", diags)
	}
	found := false
	for _, d := range diags {
		if d.Severity == diagnostic.Warning && strings.Contains(d.Message, "expired") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an 'expired' warning, got: %v", diags)
	}
}

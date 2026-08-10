package testrunner

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestFilterByNameSubstring(t *testing.T) {
	results := []TestResult{
		{Name: "Overdue service flags only overdue vehicles"},
		{Name: "Anomaly detection flags outliers"},
		{Name: "Parts stock-out only considers active stock"},
	}

	got := FilterByName(results, "Anomaly")
	if len(got) != 1 || got[0].Name != "Anomaly detection flags outliers" {
		t.Fatalf("filter Anomaly: got %v", got)
	}

	got = FilterByName(results, "flags")
	if len(got) != 2 {
		t.Fatalf("filter flags: want 2, got %d", len(got))
	}

	got = FilterByName(results, "")
	if len(got) != 3 {
		t.Fatalf("empty filter: want all, got %d", len(got))
	}

	got = FilterByName(results, "no-such-thing")
	if len(got) != 0 {
		t.Fatalf("no-match filter: want 0, got %d", len(got))
	}
}

func TestWriteJUnitIncludesFailures(t *testing.T) {
	suites := []JUnitSuite{
		{
			File: "fleet.tln.test",
			Results: []TestResult{
				{Name: "pass case", Passed: true, Duration: 2 * time.Millisecond},
				{
					Name:     "fail case",
					Passed:   false,
					Duration: 3 * time.Millisecond,
					Errors:   []string{"expected 42, got 0", "threshold off"},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := WriteJUnit(&buf, suites); err != nil {
		t.Fatalf("WriteJUnit: %v", err)
	}
	out := buf.String()

	mustContain := []string{
		`<?xml`,
		`<testsuite name="fleet.tln.test" tests="2" failures="1"`,
		`<testcase name="pass case"`,
		`<testcase name="fail case"`,
		`<failure message="expected 42, got 0; threshold off">`,
		"expected 42, got 0&#xA;threshold off",
	}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Errorf("junit output missing %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, `<testcase name="pass case" classname="fleet.tln.test" time="0.000"><failure`) {
		t.Errorf("pass case must not contain <failure>")
	}
}

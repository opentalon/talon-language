package testrunner

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/opentalon/talon-language/internal/lexer"
	talonlog "github.com/opentalon/talon-language/internal/log"
	"github.com/opentalon/talon-language/internal/parser"
	"github.com/opentalon/talon-language/internal/planner"
	"github.com/opentalon/talon-language/internal/validator"
)

// installLogCapture swaps in a JSON logger writing to a buffer for the
// duration of one test. Returns the buffer + a decoder that reads each
// emitted record back as map[string]any.
func installLogCapture(t *testing.T) (*bytes.Buffer, func() []map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	talonlog.SetDefault(slog.New(h))
	t.Cleanup(func() { talonlog.SetDefault(nil) })
	return &buf, func() []map[string]any {
		var out []map[string]any
		for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
			if line == "" {
				continue
			}
			var rec map[string]any
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatalf("non-JSON log line: %q (%v)", line, err)
			}
			out = append(out, rec)
		}
		return out
	}
}

// Per-row logger emission inside a detect block: a `logger.info`
// statement fires once per flagged entity, with the row's data
// interpolated through the same template engine label uses.
func TestDetectLoggerFiresPerRow(t *testing.T) {
	_, records := installLogCapture(t)

	src := `
detect "Active items" {
  for records where type == "item" and status == "active"
  flag matching items
  label "active: {item.name}"
  logger.info "fired for {item.name}"
}

test "two items" {
  given {
    record 1 type "item" status "active"
    attr 1 "name" "Alpha"
    record 2 type "item" status "active"
    attr 2 "name" "Beta"
    record 3 type "item" status "defective"
    attr 3 "name" "Gamma"
  }
  when detect "Active items"
  expect {
    flagged 1
    flagged 2
  }
}`
	results := runLoggerTest(t, src)
	if len(results) != 1 || !results[0].Passed {
		t.Fatalf("test should pass: %+v", results)
	}
	// Two flagged rows → two block_logger records (plus a block_eval).
	recs := records()
	var blockLogger []map[string]any
	for _, r := range recs {
		if r["source"] == "block_logger" {
			blockLogger = append(blockLogger, r)
		}
	}
	if len(blockLogger) != 2 {
		t.Fatalf("want 2 block_logger records, got %d (%v)", len(blockLogger), recs)
	}
	// Each carries the row's name in the rendered message and the block name.
	msgs := []string{}
	for _, r := range blockLogger {
		msgs = append(msgs, r["msg"].(string))
		if r["block"] != "Active items" {
			t.Errorf("block: got %v", r["block"])
		}
	}
	gotMsgs := strings.Join(msgs, ",")
	if !strings.Contains(gotMsgs, "fired for Alpha") || !strings.Contains(gotMsgs, "fired for Beta") {
		t.Errorf("missing per-row interpolation; got %v", gotMsgs)
	}
}

// Logger level threading: warn/error levels reach the slog handler at
// their declared severity, so log aggregators can filter on them.
func TestRuleLoggerHonoursLevel(t *testing.T) {
	_, records := installLogCapture(t)

	src := `
rule "Audit deletes" {
  for records where type == "item"
  block "x"
  logger.warn "auditing {item.name}"
}

test "fires" {
  given {
    record 1 type "item"
    attr 1 "name" "Drill"
  }
  when rule "Audit deletes"
  expect {
    flagged 1
  }
}`
	if results := runLoggerTest(t, src); !results[0].Passed {
		t.Fatalf("test should pass: %+v", results)
	}
	for _, r := range records() {
		if r["source"] == "block_logger" {
			if r["level"] != "WARN" {
				t.Errorf("want WARN level, got %v", r["level"])
			}
			if r["msg"] != "auditing Drill" {
				t.Errorf("msg: %v", r["msg"])
			}
			return
		}
	}
	t.Error("no block_logger record found")
}

func runLoggerTest(t *testing.T, src string) []TestResult {
	t.Helper()
	tokens, ld := lexer.Lex("t.talon", src)
	if ld.HasErrors() {
		t.Fatalf("lex: %v", ld)
	}
	prog, pd := parser.Parse("t.talon", tokens)
	if pd.HasErrors() {
		t.Fatalf("parse: %v", pd)
	}
	if vd := validator.Validate("t.talon", prog); vd.HasErrors() {
		t.Fatalf("validate: %v", vd)
	}
	plans, planDiags := planner.Plan(prog)
	if planDiags.HasErrors() {
		t.Fatalf("plan: %v", planDiags)
	}
	return Run(prog, plans)
}

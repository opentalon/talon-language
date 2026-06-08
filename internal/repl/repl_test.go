package repl

import (
	"bytes"
	"strings"
	"testing"
)

// runScript drives the REPL with the given input lines and returns
// everything written to the output writer.
func runScript(t *testing.T, input string) string {
	t.Helper()
	var out bytes.Buffer
	if err := Run(strings.NewReader(input), &out); err != nil {
		t.Fatalf("REPL exited with error: %v", err)
	}
	return out.String()
}

func TestREPLQuitImmediately(t *testing.T) {
	out := runScript(t, ":quit\n")
	if !strings.Contains(out, "talon>") {
		t.Errorf("expected prompt, got %q", out)
	}
}

func TestREPLHelp(t *testing.T) {
	out := runScript(t, ":help\n:quit\n")
	for _, want := range []string{":facts", ":eval", ":find", ":trace", ":load"} {
		if !strings.Contains(out, want) {
			t.Errorf("help output missing %q\n%s", want, out)
		}
	}
}

func TestREPLRecordAndAttrAssertion(t *testing.T) {
	out := runScript(t, `record 501 type "item" status "active"
attr 501 "name" "Test"
attr 501 "km" 50000
:facts
:quit
`)
	if !strings.Contains(out, "OK: record 501") {
		t.Errorf("record assertion did not confirm:\n%s", out)
	}
	if !strings.Contains(out, "OK: attr 501") {
		t.Errorf("attr assertion did not confirm:\n%s", out)
	}
	if !strings.Contains(out, `attr 501 "name"`) {
		t.Errorf(":facts missing the asserted attribute:\n%s", out)
	}
}

func TestREPLInlineBlockEvalAndTrace(t *testing.T) {
	out := runScript(t, `record 501 type "item" status "active"
attr 501 "name" "Test"
attr 501 "km" 50000
attr 501 "last_service_km" 25000
detect "Service overdue" {
  for records where type == "item"
    and attr "km" > attr "last_service_km" + 20000
  flag matching items
  label "{item.name} overdue"
  priority HIGH
}
:eval "Service overdue"
:trace "Service overdue"
:quit
`)
	if !strings.Contains(out, `OK: detect "Service overdue"`) {
		t.Errorf("block was not registered:\n%s", out)
	}
	if !strings.Contains(out, `"Service overdue": 1 detection(s) — records [501]`) {
		t.Errorf("eval output unexpected:\n%s", out)
	}
	if !strings.Contains(out, "trace:") {
		t.Errorf("trace output missing step list:\n%s", out)
	}
	if !strings.Contains(out, "step 1") {
		t.Errorf("trace missing step 1:\n%s", out)
	}
}

func TestREPLFindAndCount(t *testing.T) {
	out := runScript(t, `record 501 type "item" status "active"
record 502 type "item" status "defective"
record 503 type "person" status "active"
:find for records where type == "item"
:count for records where type == "item"
:quit
`)
	// :find prints IDs one per line; :count prints just the integer.
	if !strings.Contains(out, "  501\n") || !strings.Contains(out, "  502\n") {
		t.Errorf(":find missed expected IDs:\n%s", out)
	}
	if !strings.Contains(out, "  2\n") {
		t.Errorf(":count expected 2, output:\n%s", out)
	}
}

func TestREPLFindAcceptsBareCondition(t *testing.T) {
	// :find should accept `type == "x"` without the `for records where`
	// boilerplate — the REPL fills that in.
	out := runScript(t, `record 501 type "item"
record 502 type "thing"
:count type == "item"
:quit
`)
	if !strings.Contains(out, "  1\n") {
		t.Errorf(":count with bare condition expected 1, got:\n%s", out)
	}
}

func TestREPLLoadFile(t *testing.T) {
	out := runScript(t, ":load testdata/load_example.talon\n:rules\n:quit\n")
	if !strings.Contains(out, "loaded testdata/load_example.talon") {
		t.Errorf(":load did not confirm:\n%s", out)
	}
	if !strings.Contains(out, `"Loaded block"`) {
		t.Errorf(":rules missing the detect block from the loaded file:\n%s", out)
	}
}

func TestREPLClearAndClearFacts(t *testing.T) {
	out := runScript(t, `record 501 type "item"
detect "All items" {
  for records where type == "item"
  flag matching items
}
:clear facts
:facts
:rules
:clear
:rules
:quit
`)
	if !strings.Contains(out, "cleared facts; blocks kept") {
		t.Errorf(":clear facts did not confirm:\n%s", out)
	}
	if !strings.Contains(out, "no facts in memory") {
		t.Errorf(":facts after :clear facts should report empty:\n%s", out)
	}
	// :rules after :clear facts should still show the detect block.
	if !strings.Contains(out, `"All items"`) {
		t.Errorf(":rules after :clear facts should still list the block:\n%s", out)
	}
	if !strings.Contains(out, "cleared facts, blocks, and context") {
		t.Errorf(":clear did not confirm:\n%s", out)
	}
}

func TestREPLMultiLineBlockContinuation(t *testing.T) {
	// The continuation prompt `..` should appear while braces are unbalanced.
	out := runScript(t, `detect "Open" {
  for records where type == "item"
  flag matching items
}
:rules
:quit
`)
	if !strings.Contains(out, "  .. ") {
		t.Errorf("expected continuation prompt during multi-line block, got:\n%s", out)
	}
	if !strings.Contains(out, `"Open"`) {
		t.Errorf("block did not register:\n%s", out)
	}
}

func TestREPLParseErrorRecovery(t *testing.T) {
	// A syntactically bad line should print an error and then accept new
	// input — the REPL must not crash or lose its session.
	out := runScript(t, `rule "missing action" {
  when tool_action contains "x"
}
record 501 type "item"
:facts
:quit
`)
	if !strings.Contains(out, "error:") {
		t.Errorf("expected a validator error for the bad rule:\n%s", out)
	}
	if !strings.Contains(out, "OK: record 501") {
		t.Errorf("session did not survive the error:\n%s", out)
	}
}

func TestREPLContextSetAndList(t *testing.T) {
	out := runScript(t, `:context role "engineer"
:context
:quit
`)
	if !strings.Contains(out, "context.role = \"engineer\"") {
		t.Errorf(":context set did not confirm:\n%s", out)
	}
	if !strings.Contains(out, `role = "engineer"`) {
		t.Errorf(":context list missing the value:\n%s", out)
	}
}

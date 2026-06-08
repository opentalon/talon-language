package log

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// installCapture swaps in a JSON logger writing to a buffer and returns
// the buffer + a function that decodes each emitted record.
func installCapture(t *testing.T, level slog.Level) (*bytes.Buffer, func() []map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level})
	SetDefault(slog.New(h))
	t.Cleanup(func() { SetDefault(nil) })
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

func TestParseFormat(t *testing.T) {
	cases := map[string]Format{"": FormatText, "text": FormatText, "TEXT": FormatText, "json": FormatJSON, "JSON": FormatJSON}
	for in, want := range cases {
		got, err := ParseFormat(in)
		if err != nil {
			t.Errorf("ParseFormat(%q): unexpected err %v", in, err)
		}
		if got != want {
			t.Errorf("ParseFormat(%q): got %v want %v", in, got, want)
		}
	}
	if _, err := ParseFormat("yaml"); err == nil {
		t.Error("ParseFormat(yaml): expected error")
	}
}

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"":      slog.LevelInfo,
		"info":  slog.LevelInfo,
		"INFO":  slog.LevelInfo,
		"debug": slog.LevelDebug,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}
	for in, want := range cases {
		got, err := ParseLevel(in)
		if err != nil {
			t.Errorf("ParseLevel(%q): unexpected err %v", in, err)
		}
		if got != want {
			t.Errorf("ParseLevel(%q): got %v want %v", in, got, want)
		}
	}
	if _, err := ParseLevel("loud"); err == nil {
		t.Error("ParseLevel(loud): expected error")
	}
}

func TestBlockEventShape(t *testing.T) {
	_, records := installCapture(t, slog.LevelDebug)
	BlockEval(context.Background(), "Service overdue", "detect", 3, 12*time.Millisecond)

	recs := records()
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	r := recs[0]
	if r["msg"] != "block_eval" {
		t.Errorf("msg: %v", r["msg"])
	}
	if r["rule"] != "Service overdue" {
		t.Errorf("rule: %v", r["rule"])
	}
	if r["type"] != "detect" {
		t.Errorf("type: %v", r["type"])
	}
	// JSON numbers decode to float64.
	if r["matched"].(float64) != 3 {
		t.Errorf("matched: %v", r["matched"])
	}
	if r["duration_ms"].(float64) != 12 {
		t.Errorf("duration_ms: %v", r["duration_ms"])
	}
}

func TestBlockEvalOmitsKindWhenEmpty(t *testing.T) {
	_, records := installCapture(t, slog.LevelDebug)
	BlockEval(context.Background(), "x", "", 0, 0)
	r := records()[0]
	if _, ok := r["type"]; ok {
		t.Errorf("type attr should be omitted when kind is empty, got %v", r)
	}
}

func TestMCPCallOK(t *testing.T) {
	_, records := installCapture(t, slog.LevelDebug)
	MCPCall(context.Background(), "inventory", "list-items", "ok", 30*time.Millisecond, nil)
	r := records()[0]
	if r["level"] != "INFO" {
		t.Errorf("level: %v", r["level"])
	}
	if r["plugin"] != "inventory" || r["action"] != "list-items" || r["status"] != "ok" {
		t.Errorf("attrs: %+v", r)
	}
	if r["duration_ms"].(float64) != 30 {
		t.Errorf("duration_ms: %v", r["duration_ms"])
	}
	if _, ok := r["error"]; ok {
		t.Errorf("ok call should not carry an error attribute, got %v", r)
	}
}

func TestMCPCallError(t *testing.T) {
	_, records := installCapture(t, slog.LevelDebug)
	MCPCall(context.Background(), "inventory", "delete-item", "error", time.Millisecond, errors.New("boom"))
	r := records()[0]
	if r["level"] != "ERROR" {
		t.Errorf("level: %v want ERROR", r["level"])
	}
	if r["error"] != "boom" {
		t.Errorf("error: %v", r["error"])
	}
}

func TestLevelFiltering(t *testing.T) {
	_, records := installCapture(t, slog.LevelWarn)
	BlockEval(context.Background(), "x", "detect", 0, 0)
	if recs := records(); len(recs) != 0 {
		t.Errorf("INFO record should be filtered at LevelWarn, got %v", recs)
	}
}

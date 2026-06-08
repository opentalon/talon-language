package reactive

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/opentalon/talon-language/internal/ast"
	"github.com/opentalon/talon-language/internal/factstore"
	talonlog "github.com/opentalon/talon-language/internal/log"
)

func captureLogs(t *testing.T) (*bytes.Buffer, func() []map[string]any) {
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

func TestLoggingActionHandlerEmitsLoggerAction(t *testing.T) {
	_, records := captureLogs(t)
	d := New(LoggingActionHandler())

	d.Register(&ast.OnBlock{
		Name:    `on change attr "current_stock"`,
		Trigger: "change",
		Attr:    "current_stock",
		Actions: []ast.OnAction{
			&ast.LoggerAction{
				Level:   "warn",
				Message: ast.Template{Raw: "stock changed for entity {event.entity}: {event.prev} -> {event.value}"},
			},
		},
	})

	var emitter factstore.EventEmitter
	d.Subscribe(&emitter)

	emitter.Emit(context.Background(), factstore.Event{
		Kind: factstore.EventChange,
		Fact: factstore.Fact{Entity: "501", Attribute: "current_stock", Value: 10},
		Prev: factstore.Fact{Entity: "501", Attribute: "current_stock", Value: 20},
	})

	recs := records()
	if len(recs) != 1 {
		t.Fatalf("expected 1 log record, got %d (%v)", len(recs), recs)
	}
	r := recs[0]
	if r["level"] != "WARN" {
		t.Errorf("level: %v want WARN", r["level"])
	}
	if r["msg"] != "stock changed for entity 501: 20 -> 10" {
		t.Errorf("msg: %v", r["msg"])
	}
	if r["source"] != "on_block" {
		t.Errorf("source: %v", r["source"])
	}
	if r["trigger"] != "change" {
		t.Errorf("trigger: %v", r["trigger"])
	}
}

func TestLoggingActionHandlerLevels(t *testing.T) {
	_, records := captureLogs(t)
	d := New(LoggingActionHandler())

	for _, level := range []string{"info", "warn", "error"} {
		d.Register(&ast.OnBlock{
			Name:    "block-" + level,
			Trigger: "assert",
			Actions: []ast.OnAction{
				&ast.LoggerAction{Level: level, Message: ast.Template{Raw: level + " hit"}},
			},
		})
	}

	var emitter factstore.EventEmitter
	d.Subscribe(&emitter)
	emitter.Emit(context.Background(), factstore.Event{
		Kind: factstore.EventAssert,
		Fact: factstore.Fact{Attribute: "type", Value: "item"},
	})

	recs := records()
	if len(recs) != 3 {
		t.Fatalf("expected 3 records, got %d", len(recs))
	}
	want := map[string]string{
		"info hit":  "INFO",
		"warn hit":  "WARN",
		"error hit": "ERROR",
	}
	for _, r := range recs {
		msg := r["msg"].(string)
		if r["level"] != want[msg] {
			t.Errorf("msg %q: level %v want %v", msg, r["level"], want[msg])
		}
	}
}

func TestLoggingActionHandlerBlockRef(t *testing.T) {
	_, records := captureLogs(t)
	d := New(LoggingActionHandler())

	d.Register(&ast.OnBlock{
		Name:    "block-ref",
		Trigger: "assert",
		Actions: []ast.OnAction{
			&ast.BlockRefAction{Kind: "recommend", Name: "Order stock"},
		},
	})

	var emitter factstore.EventEmitter
	d.Subscribe(&emitter)
	emitter.Emit(context.Background(), factstore.Event{
		Kind: factstore.EventAssert,
		Fact: factstore.Fact{Attribute: "type", Value: "item"},
	})

	recs := records()
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	r := recs[0]
	if r["msg"] != "on_block_ref" {
		t.Errorf("msg: %v", r["msg"])
	}
	if r["ref_name"] != "Order stock" || r["ref_kind"] != "recommend" {
		t.Errorf("ref attrs: %+v", r)
	}
}

// Package log is the runtime observability surface for Talon. It wraps
// log/slog with two ergonomic affordances:
//
//  1. Init(format, level) configures the package-default logger from CLI
//     flags. The executor, planner, reactive dispatcher, and `on { }`
//     LoggerAction all funnel through Default() so a single CLI invocation
//     decides where logs go.
//
//  2. Event helpers (BlockEval, MCPCall) emit records with the canonical
//     attribute set the observability RFC (#20) defines. Callers that need
//     ad-hoc structured logging fall back to the slog API.
//
// All output goes to stderr so it can never pollute `talon run` or REPL
// stdout (which carries the actual detection/decision payload).
package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// Format selects the slog handler.
type Format int

const (
	// FormatText is slog's TextHandler — key=value pairs, intended for
	// humans reading a terminal.
	FormatText Format = iota
	// FormatJSON is slog's JSONHandler — one JSON object per line,
	// intended for log aggregators (Loki, etc.).
	FormatJSON
)

// ParseFormat accepts "text" or "json" (case-insensitive). Returns the
// default (FormatText) and an error for any other input.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "text":
		return FormatText, nil
	case "json":
		return FormatJSON, nil
	}
	return FormatText, fmt.Errorf("unknown log format %q (want text or json)", s)
}

// ParseLevel accepts debug/info/warn/error (case-insensitive). Returns the
// default (LevelInfo) and an error for any other input.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return slog.LevelInfo, fmt.Errorf("unknown log level %q (want debug/info/warn/error)", s)
}

var (
	mu      sync.RWMutex
	current *slog.Logger
)

// Init builds and installs the package-default logger. Call once at CLI
// startup; subsequent calls replace the default. Safe for concurrent use,
// though callers usually only invoke this from main.
func Init(format Format, level slog.Level, w io.Writer) {
	if w == nil {
		w = os.Stderr
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	switch format {
	case FormatJSON:
		h = slog.NewJSONHandler(w, opts)
	default:
		h = slog.NewTextHandler(w, opts)
	}
	mu.Lock()
	current = slog.New(h)
	mu.Unlock()
}

// Default returns the active logger. If Init has not been called, a
// stderr text logger at LevelInfo is created lazily so call-sites can
// always rely on a non-nil logger.
func Default() *slog.Logger {
	mu.RLock()
	l := current
	mu.RUnlock()
	if l != nil {
		return l
	}
	Init(FormatText, slog.LevelInfo, os.Stderr)
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// SetDefault swaps in a caller-built logger. Test fixtures use this to
// capture output into a bytes.Buffer.
func SetDefault(l *slog.Logger) {
	mu.Lock()
	current = l
	mu.Unlock()
}

// ─── Event helpers ───────────────────────────────────────────────────────────

// BlockEval emits one info-level record summarising a block's execution.
// The attribute names follow the RFC: rule, matched, duration_ms. The
// optional `kind` argument is included when non-empty (e.g. "detect",
// "rule") — callers that don't know the kind at the call-site pass "".
//
// Callers time around the block run themselves and pass the duration:
//
//	start := time.Now()
//	... run ...
//	log.BlockEval(ctx, name, kind, matched, time.Since(start))
//
// The log package never times anything itself — that ambiguity hides
// bugs.
func BlockEval(ctx context.Context, name, kind string, matched int, duration time.Duration) {
	attrs := []slog.Attr{
		slog.String("rule", name),
		slog.Int("matched", matched),
		slog.Int64("duration_ms", duration.Milliseconds()),
	}
	if kind != "" {
		// Insert as the second attribute so logs read rule → type → counts.
		attrs = append([]slog.Attr{attrs[0], slog.String("type", kind)}, attrs[1:]...)
	}
	Default().LogAttrs(ctx, slog.LevelInfo, "block_eval", attrs...)
}

// MCPCall emits one info-level record for a successful MCP tool call, or
// an error-level record when err is non-nil. Status is "ok", "error", or
// "skipped" (e.g. confirmation_denied).
func MCPCall(ctx context.Context, server, tool, status string, duration time.Duration, err error) {
	level := slog.LevelInfo
	attrs := []slog.Attr{
		slog.String("plugin", server),
		slog.String("action", tool),
		slog.String("status", status),
		slog.Int64("duration_ms", duration.Milliseconds()),
	}
	if err != nil {
		level = slog.LevelError
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	Default().LogAttrs(ctx, level, "mcp_call", attrs...)
}

// RuleAction emits one record for a policy-rule outcome (block/allow). The
// audit layer in a follow-up subscribes to this event class.
func RuleAction(ctx context.Context, rule, action, tool, reason string) {
	Default().LogAttrs(ctx, slog.LevelInfo, "rule_action",
		slog.String("rule", rule),
		slog.String("action", action),
		slog.String("tool", tool),
		slog.String("reason", reason),
	)
}

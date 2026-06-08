package repl

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/opentalon/talon-language/internal/ast"
)

// braceBalance counts opening minus closing braces in src, ignoring those
// inside `"..."` strings, `// ...` line comments, and `/* ... */` block
// comments. A positive return value means we're still inside a block and
// the REPL should prompt for more input.
func braceBalance(src string) int {
	depth := 0
	inString := false
	inLineComment := false
	inBlockComment := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case inLineComment:
			if c == '\n' {
				inLineComment = false
			}
		case inBlockComment:
			if c == '*' && i+1 < len(src) && src[i+1] == '/' {
				inBlockComment = false
				i++
			}
		case inString:
			if c == '\\' && i+1 < len(src) {
				i++ // skip escaped char
				continue
			}
			if c == '"' {
				inString = false
			}
		default:
			switch {
			case c == '"':
				inString = true
			case c == '/' && i+1 < len(src) && src[i+1] == '/':
				inLineComment = true
				i++
			case c == '/' && i+1 < len(src) && src[i+1] == '*':
				inBlockComment = true
				i++
			case c == '{':
				depth++
			case c == '}':
				depth--
			}
		}
	}
	return depth
}

// classify reports what kind of input a complete (brace-balanced) buffer is.
type inputKind int

const (
	inputEmpty inputKind = iota
	inputCommand
	inputFactRecord
	inputFactAttr
	inputBlock
)

func classify(buf string) inputKind {
	trimmed := strings.TrimSpace(buf)
	if trimmed == "" {
		return inputEmpty
	}
	if strings.HasPrefix(trimmed, ":") {
		return inputCommand
	}
	// Fact-assertion shortcuts must be a single line that begins with the
	// keyword. `record` or `attr` followed by an integer ID — otherwise the
	// input goes through the language parser, which can also legitimately
	// start with those keywords in other contexts.
	if !strings.Contains(trimmed, "\n") {
		head := strings.Fields(trimmed)
		if len(head) >= 2 {
			if (head[0] == "record" || head[0] == "attr") && isInteger(head[1]) {
				if head[0] == "record" {
					return inputFactRecord
				}
				return inputFactAttr
			}
		}
	}
	return inputBlock
}

func isInteger(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// parseRecordAssertion parses `record N <key> <value> [<key> <value> ...]`.
// Values may be quoted strings or numeric literals. The first `type` key
// becomes the record's type; other keys become attributes alongside it.
func parseRecordAssertion(line string) (ast.TestDatum, error) {
	rest, head, err := takeWord(line, "record")
	if err != nil {
		return ast.TestDatum{}, err
	}
	_ = head
	id, rest, err := takeID(rest)
	if err != nil {
		return ast.TestDatum{}, err
	}
	fields, err := parseKVPairs(rest)
	if err != nil {
		return ast.TestDatum{}, err
	}
	if len(fields) == 0 {
		return ast.TestDatum{}, fmt.Errorf("record %d: expected at least one key/value pair", id)
	}
	return ast.TestDatum{Kind: "record", ID: id, Fields: fields}, nil
}

// parseAttrAssertion parses `attr N "key" <value>`. Exactly one key/value
// pair — multiple are allowed but typical REPL use sticks with one.
func parseAttrAssertion(line string) (ast.TestDatum, error) {
	rest, _, err := takeWord(line, "attr")
	if err != nil {
		return ast.TestDatum{}, err
	}
	id, rest, err := takeID(rest)
	if err != nil {
		return ast.TestDatum{}, err
	}
	key, rest, err := takeString(rest)
	if err != nil {
		return ast.TestDatum{}, err
	}
	val, _, err := takeValue(rest)
	if err != nil {
		return ast.TestDatum{}, fmt.Errorf("attr %d %q: %w", id, key, err)
	}
	return ast.TestDatum{Kind: "attr", ID: id, Fields: map[string]interface{}{key: val}}, nil
}

func takeWord(s, expect string) (rest, got string, err error) {
	s = strings.TrimLeft(s, " \t")
	if !strings.HasPrefix(s, expect) {
		return s, "", fmt.Errorf("expected %q at start of input", expect)
	}
	return s[len(expect):], expect, nil
}

func takeID(s string) (int, string, error) {
	s = strings.TrimLeft(s, " \t")
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, s, fmt.Errorf("expected integer record ID")
	}
	n, err := strconv.Atoi(s[:end])
	if err != nil {
		return 0, s, err
	}
	return n, s[end:], nil
}

func takeString(s string) (string, string, error) {
	s = strings.TrimLeft(s, " \t")
	if !strings.HasPrefix(s, `"`) {
		return "", s, fmt.Errorf("expected quoted string")
	}
	end := 1
	for end < len(s) {
		if s[end] == '\\' && end+1 < len(s) {
			end += 2
			continue
		}
		if s[end] == '"' {
			val := s[1:end]
			return val, s[end+1:], nil
		}
		end++
	}
	return "", s, fmt.Errorf("unterminated string literal")
}

// takeValue reads a quoted string, a boolean, or a number.
func takeValue(s string) (any, string, error) {
	s = strings.TrimLeft(s, " \t")
	if s == "" {
		return nil, s, fmt.Errorf("expected value")
	}
	if s[0] == '"' {
		return takeString(s)
	}
	if strings.HasPrefix(s, "true") {
		return true, s[len("true"):], nil
	}
	if strings.HasPrefix(s, "false") {
		return false, s[len("false"):], nil
	}
	// Number — read digits, dots, leading minus.
	end := 0
	if s[0] == '-' {
		end = 1
	}
	hasDot := false
	for end < len(s) {
		c := s[end]
		if c == '.' && !hasDot {
			hasDot = true
			end++
			continue
		}
		if c < '0' || c > '9' {
			break
		}
		end++
	}
	if end == 0 || (end == 1 && s[0] == '-') {
		return nil, s, fmt.Errorf("unrecognised value %q", s)
	}
	f, err := strconv.ParseFloat(s[:end], 64)
	if err != nil {
		return nil, s, err
	}
	return f, s[end:], nil
}

// parseKVPairs reads `<key> <value> <key> <value> ...` until end of input.
// Keys are bare identifiers; values use takeValue.
func parseKVPairs(s string) (map[string]interface{}, error) {
	fields := map[string]interface{}{}
	for {
		s = strings.TrimLeft(s, " \t")
		if s == "" {
			return fields, nil
		}
		// Key is either a quoted string (for arbitrary attribute names) or
		// a bare identifier (for the common cases: type, category, status).
		var key string
		if s[0] == '"' {
			k, rest, err := takeString(s)
			if err != nil {
				return nil, err
			}
			key, s = k, rest
		} else {
			end := 0
			for end < len(s) && !isBlank(s[end]) {
				end++
			}
			key = s[:end]
			s = s[end:]
		}
		if key == "" {
			return nil, fmt.Errorf("empty key")
		}
		val, rest, err := takeValue(s)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", key, err)
		}
		fields[key] = val
		s = rest
	}
}

func isBlank(b byte) bool { return b == ' ' || b == '\t' }

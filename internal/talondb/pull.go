package talondb

import (
	"fmt"
	"strings"
)

// pullPattern is the parsed form of a Datalog pull-pattern body. The
// adapter today supports two shapes:
//
//	[:attr1 :attr2 ...]   — flat attribute list
//	[:*]                  — wildcard, project every attr on the entity
//
// Nested pull `{:attr N}` returns an unsupported error during parsing.
// The Datalevin backend handles nested pull natively; the adapter can
// add it later by recursing into related-entity Gets when consumers
// need it.
type pullPattern struct {
	wildcard bool
	attrs    []string
}

// parsePullPattern accepts the bracketed pull body. Whitespace-
// separated. Leading/trailing brackets are stripped first.
//
// Error categories:
//   - missing brackets / unbalanced → invalid syntax
//   - contains `{...}` (nested pull) → unsupported (typed)
//   - empty list → invalid (no attrs to project)
func parsePullPattern(s string) (*pullPattern, error) {
	body := strings.TrimSpace(s)
	if !strings.HasPrefix(body, "[") || !strings.HasSuffix(body, "]") {
		return nil, fmt.Errorf("talondb adapter: pull pattern must be bracketed, got %q", s)
	}
	body = strings.TrimSpace(body[1 : len(body)-1])
	if strings.Contains(body, "{") || strings.Contains(body, "}") {
		return nil, fmt.Errorf("talondb adapter: nested pull (`{:attr N}`) not supported; got %q", s)
	}
	if body == "" {
		return nil, fmt.Errorf("talondb adapter: pull pattern is empty")
	}

	out := &pullPattern{}
	for _, tok := range strings.Fields(body) {
		if tok == ":*" || tok == "*" {
			out.wildcard = true
			continue
		}
		// Drop a trailing comma if the caller wrote a Clojure-style
		// comma-separated form.
		tok = strings.TrimSuffix(tok, ",")
		if tok == "" {
			continue
		}
		out.attrs = append(out.attrs, tok)
	}
	if out.wildcard && len(out.attrs) > 0 {
		// `[:* :record/name]` is valid in Datalog (`:*` plus explicit
		// adds) but pointless in the flat case — `:*` already
		// projects everything. Treat as wildcard for simplicity.
		out.attrs = nil
	}
	if !out.wildcard && len(out.attrs) == 0 {
		return nil, fmt.Errorf("talondb adapter: pull pattern parsed to no attributes")
	}
	return out, nil
}

// project applies the pattern to a doc, returning a new map with only
// the requested attributes (wildcard returns the doc itself, unaliased,
// so callers shouldn't mutate it).
func (p *pullPattern) project(doc map[string]any) map[string]any {
	if p.wildcard {
		return doc
	}
	out := make(map[string]any, len(p.attrs))
	for _, attr := range p.attrs {
		if v, ok := doc[attr]; ok {
			out[attr] = v
		}
	}
	return out
}

// Package mod parses the tln project manifest, `mod.tln` — the Bundler-style
// declaration of which plugins a project uses (ADR 0013). Each line is:
//
//	plugin "<name>" "<version>" [store] [from "<module>"]
//
// `<name>` is the identifier a program references in `connector "…" via <name>`.
// The backing Go module defaults to github.com/opentalon/tln-<name>, overridable
// with `from`. `store` marks a FactStore plugin (default is a tool plugin).
// Comments start with `#` or `//`; blank lines are ignored.
package mod

import (
	"fmt"
	"strings"
)

// Plugin is one manifest entry.
type Plugin struct {
	Name    string // connector identifier (e.g. "mcp")
	Version string // module version (e.g. "v0.1.0")
	Module  string // Go module path
	Store   bool   // FactStore plugin vs the default tool plugin
}

// Manifest is a parsed mod.tln.
type Manifest struct {
	Plugins []Plugin
}

// DefaultModule is the module path convention for an unqualified plugin name:
// github.com/opentalon/tln-<name>. Override per entry with `from "<module>"`.
func DefaultModule(name string) string { return "github.com/opentalon/tln-" + name }

// Parse reads mod.tln source into a [Manifest]. It errors on a malformed line,
// a duplicate plugin name, or a missing version.
func Parse(src string) (*Manifest, error) {
	m := &Manifest{}
	seen := map[string]bool{}
	for i, raw := range strings.Split(src, "\n") {
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}
		p, err := parseLine(line)
		if err != nil {
			return nil, fmt.Errorf("mod.tln:%d: %w", i+1, err)
		}
		if seen[p.Name] {
			return nil, fmt.Errorf("mod.tln:%d: duplicate plugin %q", i+1, p.Name)
		}
		seen[p.Name] = true
		m.Plugins = append(m.Plugins, p)
	}
	return m, nil
}

func stripComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		line = line[:i]
	}
	if i := strings.Index(line, "#"); i >= 0 {
		line = line[:i]
	}
	return line
}

// parseLine parses one `plugin "name" "version" [store] [from "module"]` line.
func parseLine(line string) (Plugin, error) {
	toks, err := tokenize(line)
	if err != nil {
		return Plugin{}, err
	}
	if len(toks) < 3 || toks[0] != "plugin" {
		return Plugin{}, fmt.Errorf("expected `plugin \"name\" \"version\" [store] [from \"module\"]`")
	}
	p := Plugin{Name: toks[1], Version: toks[2]}
	if p.Name == "" {
		return Plugin{}, fmt.Errorf("plugin name is empty")
	}
	if p.Version == "" {
		return Plugin{}, fmt.Errorf("plugin %q is missing a version", p.Name)
	}
	rest := toks[3:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "store":
			p.Store = true
		case "from":
			if i+1 >= len(rest) {
				return Plugin{}, fmt.Errorf("`from` needs a module path")
			}
			p.Module = rest[i+1]
			i++
		default:
			return Plugin{}, fmt.Errorf("unexpected token %q", rest[i])
		}
	}
	if p.Module == "" {
		p.Module = DefaultModule(p.Name)
	}
	return p, nil
}

// tokenize splits a line into bare words and "quoted strings" (quotes stripped).
func tokenize(line string) ([]string, error) {
	var toks []string
	i := 0
	for i < len(line) {
		c := line[i]
		switch c {
		case ' ', '\t', '\r':
			i++
		case '"':
			j := i + 1
			for j < len(line) && line[j] != '"' {
				j++
			}
			if j >= len(line) {
				return nil, fmt.Errorf("unterminated string")
			}
			toks = append(toks, line[i+1:j])
			i = j + 1
		default:
			j := i
			for j < len(line) && line[j] != ' ' && line[j] != '\t' && line[j] != '\r' && line[j] != '"' {
				j++
			}
			toks = append(toks, line[i:j])
			i = j
		}
	}
	return toks, nil
}

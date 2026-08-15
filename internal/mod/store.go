package mod

import (
	"fmt"
	"strings"
)

// StoreValue is one store-config value: a literal string, or an `env "VAR"`
// reference resolved at run time.
type StoreValue struct {
	Literal string
	EnvVar  string
}

// Store is a parsed config/store.tln — which store plugin backs the program and
// its connection config:
//
//	store db { target env "TLNDB_ADDR" }
//
// `db` is the store plugin (declared `store` in mod.tln); the body is free-form
// `key value` config the plugin interprets. At most one store block.
type Store struct {
	Plugin string
	Config map[string]StoreValue
}

type stok struct {
	kind int // 0 word, 1 string, 2 '{', 3 '}'
	s    string
}

// ParseStore parses config/store.tln. Returns (nil, nil) when the source has no
// store block (→ the default in-memory store).
func ParseStore(src string) (*Store, error) {
	toks, err := lexStore(src)
	if err != nil {
		return nil, err
	}
	if len(toks) == 0 {
		return nil, nil
	}
	if toks[0].kind != 0 || toks[0].s != "store" {
		return nil, fmt.Errorf("config/store.tln: expected `store <plugin> { … }`")
	}
	if len(toks) < 4 || toks[1].kind != 0 || toks[2].kind != 2 {
		return nil, fmt.Errorf("config/store.tln: expected `store <plugin> { … }`")
	}
	st := &Store{Plugin: toks[1].s, Config: map[string]StoreValue{}}
	i := 3
	for i < len(toks) && toks[i].kind != 3 {
		if toks[i].kind != 0 {
			return nil, fmt.Errorf("config/store.tln: expected a config key, got %q", toks[i].s)
		}
		key := toks[i].s
		i++
		if i >= len(toks) {
			return nil, fmt.Errorf("config/store.tln: key %q has no value", key)
		}
		switch {
		case toks[i].kind == 0 && toks[i].s == "env":
			i++
			if i >= len(toks) || toks[i].kind != 1 {
				return nil, fmt.Errorf("config/store.tln: `env` needs a \"VAR\" name")
			}
			st.Config[key] = StoreValue{EnvVar: toks[i].s}
		case toks[i].kind == 1:
			st.Config[key] = StoreValue{Literal: toks[i].s}
		default:
			return nil, fmt.Errorf("config/store.tln: value for %q must be a string or `env \"VAR\"`", key)
		}
		i++
	}
	if i >= len(toks) || toks[i].kind != 3 {
		return nil, fmt.Errorf("config/store.tln: missing closing `}`")
	}
	if i != len(toks)-1 {
		return nil, fmt.Errorf("config/store.tln: only one `store` block is allowed")
	}
	return st, nil
}

// lexStore tokenizes into words, "strings", and braces, stripping #/// comments.
func lexStore(src string) ([]stok, error) {
	var flat strings.Builder
	for _, line := range strings.Split(src, "\n") {
		flat.WriteString(stripComment(line))
		flat.WriteByte(' ')
	}
	s := flat.String()
	var toks []stok
	i := 0
	for i < len(s) {
		c := s[i]
		switch c {
		case ' ', '\t', '\r', '\n':
			i++
		case '{':
			toks = append(toks, stok{2, "{"})
			i++
		case '}':
			toks = append(toks, stok{3, "}"})
			i++
		case '"':
			j := i + 1
			for j < len(s) && s[j] != '"' {
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("config/store.tln: unterminated string")
			}
			toks = append(toks, stok{1, s[i+1 : j]})
			i = j + 1
		default:
			j := i
			for j < len(s) && !strings.ContainsRune(" \t\r\n{}\"", rune(s[j])) {
				j++
			}
			toks = append(toks, stok{0, s[i:j]})
			i = j
		}
	}
	return toks, nil
}

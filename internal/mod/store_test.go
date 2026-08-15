package mod

import "testing"

func TestParseStore_Basic(t *testing.T) {
	s, err := ParseStore(`
# the store for this project
store db {
  target env "TLNDB_ADDR"
  timeout "30s"
}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s == nil || s.Plugin != "db" {
		t.Fatalf("bad store: %+v", s)
	}
	if s.Config["target"].EnvVar != "TLNDB_ADDR" {
		t.Errorf("target should be env TLNDB_ADDR, got %+v", s.Config["target"])
	}
	if s.Config["timeout"].Literal != "30s" {
		t.Errorf("timeout should be literal 30s, got %+v", s.Config["timeout"])
	}
}

func TestParseStore_None(t *testing.T) {
	s, err := ParseStore("// just a comment\n\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s != nil {
		t.Fatalf("no store block → nil, got %+v", s)
	}
}

func TestParseStore_Errors(t *testing.T) {
	for _, src := range []string{
		`store db { target }`,                 // key without value
		`plugin db {}`,                        // wrong keyword
		`store db { target env }`,             // env without var
		`store db { target "x" } store y { }`, // two blocks
		`store db { target "x"`,               // missing closing brace
	} {
		if _, err := ParseStore(src); err == nil {
			t.Errorf("expected error for %q", src)
		}
	}
}

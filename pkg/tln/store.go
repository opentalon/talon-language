package tln

import (
	"errors"
	"os"

	"github.com/opentalon/tln-language/internal/mod"
)

// LoadStoreConfig reads a project's config/store.tln and returns the store
// plugin's connector spec, with `env "VAR"` values resolved from the
// environment. ok is false when the file is absent or declares no `store` block
// — the caller then uses the default in-memory store (Active Record: no store
// config → memory). The generated bundle calls this, then builds the store via
// the plugin's Factory and installs it with [WithFactStore].
func LoadStoreConfig(path string) (ConnectorSpec, bool, error) {
	src, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ConnectorSpec{}, false, nil
	}
	if err != nil {
		return ConnectorSpec{}, false, err
	}
	st, err := mod.ParseStore(string(src))
	if err != nil {
		return ConnectorSpec{}, false, err
	}
	if st == nil {
		return ConnectorSpec{}, false, nil
	}
	cfg := make(map[string]string, len(st.Config))
	for k, v := range st.Config {
		if v.EnvVar != "" {
			cfg[k] = os.Getenv(v.EnvVar)
		} else {
			cfg[k] = v.Literal
		}
	}
	return ConnectorSpec{Name: st.Plugin, Plugin: st.Plugin, Config: cfg}, true, nil
}

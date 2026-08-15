package executor

import (
	"context"
	"fmt"
	"sync"
)

// This file implements the "no host" leg of tln's two-mode tool routing
// (ADR 0012). When the host does NOT install a ToolResolver via
// WithToolResolver, tool calls are routed through the `connector` blocks the
// program declares: each connector names the plugin that backs a server, and
// the host registers plugin *factories* by name. Core never imports a plugin —
// it only calls the factory the host provided — so the language stays
// dependency-free while a standalone program can still reach mcp/io/etc.

// ConnectorSpec is the fully-resolved config a [PluginFactory] receives: env
// references are already resolved to their values, so a factory never touches
// the environment itself.
type ConnectorSpec struct {
	Name   string
	Plugin string
	Config map[string]string
}

// PluginFactory builds a ToolResolver for one connector from its resolved spec.
// Registered by the host via tln.WithPlugin(pluginName, factory).
type PluginFactory func(spec ConnectorSpec) (ToolResolver, error)

// EnvResolver resolves an environment variable name to its value. ok=false
// means "not set". A nil EnvResolver means environment access is denied — every
// env reference then fails, which is the deny-by-default posture sandboxes rely
// on.
type EnvResolver func(name string) (string, bool)

// ConnectorConfigValue is one connector config entry: exactly one of Literal
// (an ordinary value) or EnvVar (an `env "VAR"` reference resolved at run time).
type ConnectorConfigValue struct {
	Literal any
	EnvVar  string
}

// Connector is the executor-side view of a `connector` block, decoupled from
// the AST so this package takes no dependency on internal/ast.
type Connector struct {
	Name   string
	Plugin string
	Config map[string]ConnectorConfigValue
}

// connectorResolver routes each `tool "server" …` call to the plugin its
// connector declares. Plugins are built lazily from their factory and cached
// per server; env values are resolved through the EnvResolver (deny-by-default).
type connectorResolver struct {
	connectors map[string]Connector
	factories  map[string]PluginFactory
	env        EnvResolver

	mu    sync.Mutex
	cache map[string]ToolResolver
}

// NewConnectorResolver builds the connector-backed ToolResolver used when no
// host resolver is installed.
func NewConnectorResolver(conns []Connector, factories map[string]PluginFactory, env EnvResolver) ToolResolver {
	m := make(map[string]Connector, len(conns))
	for _, c := range conns {
		m[c.Name] = c
	}
	return &connectorResolver{
		connectors: m,
		factories:  factories,
		env:        env,
		cache:      map[string]ToolResolver{},
	}
}

var _ ToolResolver = (*connectorResolver)(nil)

// Call resolves (and caches) the plugin for `server`, then dispatches.
func (r *connectorResolver) Call(ctx context.Context, server, tool string, args map[string]any) (any, error) {
	res, err := r.resolverFor(server)
	if err != nil {
		return nil, err
	}
	return res.Call(ctx, server, tool, args)
}

func (r *connectorResolver) resolverFor(server string) (ToolResolver, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if cached, ok := r.cache[server]; ok {
		return cached, nil
	}

	conn, ok := r.connectors[server]
	if !ok {
		return nil, fmt.Errorf("tln: no connector for tool server %q — declare `connector %q via <plugin> { … }` or install a host ToolResolver", server, server)
	}
	factory, ok := r.factories[conn.Plugin]
	if !ok {
		return nil, fmt.Errorf("tln: connector %q needs plugin %q, but no factory is registered (use tln.WithPlugin(%q, …))", server, conn.Plugin, conn.Plugin)
	}

	spec := ConnectorSpec{Name: conn.Name, Plugin: conn.Plugin, Config: make(map[string]string, len(conn.Config))}
	for k, v := range conn.Config {
		if v.EnvVar != "" {
			if r.env == nil {
				return nil, fmt.Errorf("tln: connector %q reads env %q, but environment access is not permitted here", server, v.EnvVar)
			}
			val, ok := r.env(v.EnvVar)
			if !ok {
				return nil, fmt.Errorf("tln: connector %q: environment variable %q is not set", server, v.EnvVar)
			}
			spec.Config[k] = val
			continue
		}
		spec.Config[k] = fmt.Sprintf("%v", v.Literal)
	}

	res, err := factory(spec)
	if err != nil {
		return nil, fmt.Errorf("tln: building connector %q via %q: %w", server, conn.Plugin, err)
	}
	r.cache[server] = res
	return res, nil
}

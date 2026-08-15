package tln_test

import (
	"context"
	"testing"

	"github.com/opentalon/tln-language/pkg/tln"
)

// recordingResolver captures the spec its factory received and the last call.
type recordingResolver struct {
	server, tool string
	args         map[string]any
}

func (r *recordingResolver) Call(_ context.Context, server, tool string, args map[string]any) (any, error) {
	r.server, r.tool, r.args = server, tool, args
	return map[string]any{"ok": true}, nil
}

// TestConnectorRoutingResolvesEnvAndDispatches: with no host resolver, a tool
// call routes through the program's connector to the registered plugin factory,
// which receives env-resolved config and then handles the call.
func TestConnectorRoutingResolvesEnvAndDispatches(t *testing.T) {
	src := `
connector "svc" via testplugin {
  endpoint env "SVC_ENDPOINT"
  mode "live"
}
workflow "w" {
  step "call" { tool "svc" "do" { arg "x" } }
}`
	rec := &recordingResolver{}
	var gotSpec tln.ConnectorSpec
	factory := func(spec tln.ConnectorSpec) (tln.ToolResolver, error) {
		gotSpec = spec
		return rec, nil
	}
	_, err := tln.RunWorkflow(context.Background(), src,
		tln.WithPlugin("testplugin", factory),
		tln.WithEnv(func(name string) (string, bool) {
			if name == "SVC_ENDPOINT" {
				return "https://svc.local", true
			}
			return "", false
		}),
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotSpec.Plugin != "testplugin" {
		t.Errorf("factory plugin = %q, want testplugin", gotSpec.Plugin)
	}
	if gotSpec.Config["endpoint"] != "https://svc.local" {
		t.Errorf("env not resolved into config: %+v", gotSpec.Config)
	}
	if gotSpec.Config["mode"] != "live" {
		t.Errorf("literal config lost: %+v", gotSpec.Config)
	}
	if rec.server != "svc" || rec.tool != "do" {
		t.Errorf("dispatch wrong: server=%q tool=%q", rec.server, rec.tool)
	}
}

// TestConnectorEnvDeniedWithoutResolver: a connector that reads env must fail
// when no env resolver is installed (deny-by-default).
func TestConnectorEnvDeniedWithoutResolver(t *testing.T) {
	src := `
connector "svc" via testplugin { token env "SECRET" }
workflow "w" { step "s" { tool "svc" "do" { } } }`
	_, err := tln.RunWorkflow(context.Background(), src,
		tln.WithPlugin("testplugin", func(tln.ConnectorSpec) (tln.ToolResolver, error) {
			return &recordingResolver{}, nil
		}),
		// no WithEnv → environment access denied
	)
	if err == nil {
		t.Fatal("expected env to be denied without a resolver, got nil error")
	}
}

// TestHostResolverWinsOverConnectors: when a host resolver is installed it
// handles every call and connector factories are never consulted.
func TestHostResolverWinsOverConnectors(t *testing.T) {
	src := `
connector "svc" via testplugin { endpoint env "X" }
workflow "w" { step "s" { tool "svc" "do" { } } }`
	host := &recordingResolver{}
	factoryUsed := false
	_, err := tln.RunWorkflow(context.Background(), src,
		tln.WithToolResolver(host),
		tln.WithPlugin("testplugin", func(tln.ConnectorSpec) (tln.ToolResolver, error) {
			factoryUsed = true
			return &recordingResolver{}, nil
		}),
		tln.WithEnv(func(string) (string, bool) { return "v", true }),
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if host.server != "svc" {
		t.Errorf("host resolver should have received the call, got server=%q", host.server)
	}
	if factoryUsed {
		t.Error("connector factory must not be used when a host resolver is installed")
	}
}

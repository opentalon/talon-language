package executor

import "context"

// MCPCaller routes MCP tool calls to the host's plugin system.
// The host (e.g. OpenTalon orchestrator) implements this interface.
type MCPCaller interface {
	Call(ctx context.Context, server, tool string, args map[string]any) (any, error)
}

// ConfirmationHook is called before executing a workflow step.
// Return true to proceed, false to skip the step.
type ConfirmationHook func(ctx context.Context, step, server, tool string) (bool, error)

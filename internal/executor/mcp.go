package executor

import "context"

// ToolResolver routes tool calls to the host's plugin system: given a
// (server, tool, args) triple it performs the call and returns the structured
// result. tln never speaks a transport itself — a plugin (e.g. tln-mcp) or the
// host implements this interface, injected via tln.WithToolResolver.
type ToolResolver interface {
	Call(ctx context.Context, server, tool string, args map[string]any) (any, error)
}

// ConfirmationHook is called before executing a workflow step.
// Return true to proceed, false to skip the step.
type ConfirmationHook func(ctx context.Context, step, server, tool string) (bool, error)

// ApprovalHook gates a `remediate approve` MCP call on a role-based
// decision. The host implements it (e.g. routing to a manager for
// sign-off). rule is the block name for audit. Return true to proceed.
type ApprovalHook func(ctx context.Context, role, rule string, args map[string]any) (bool, error)

// QueuedCall is one MCP call deferred by `remediate queue` for the host
// to dispatch later.
type QueuedCall struct {
	Server string
	Tool   string
	Args   map[string]any
}

// Queue receives `remediate queue` calls for later, host-driven dispatch.
// batch is a free-form group name tln does not interpret.
type Queue interface {
	Enqueue(ctx context.Context, batch string, call QueuedCall) error
}

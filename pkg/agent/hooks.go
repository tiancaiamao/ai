package agent

import (
	"context"
	"log/slog"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// HookContext provides contextual information available to all hooks.
type HookContext struct {
	// Ctx is the context.Context for the current loop iteration.
	Ctx context.Context
	// AgentCtx is the agent's conversation context.
	AgentCtx *agentctx.AgentContext
	// Config is the loop configuration.
	Config *LoopConfig
}

// BeforeModelHook is called before each LLM call (including compaction-triggered ones).
// It receives the current messages and returns additional messages to append.
// Hooks are fan-out: each hook receives the same input messages; outputs are merged.
type BeforeModelHook func(hctx HookContext, messages []agentctx.AgentMessage) ([]agentctx.AgentMessage, error)

// AfterToolHook is called after each tool execution, before the result is appended to messages.
// It receives the tool result and returns a (possibly modified) result.
// Hooks are chained: the output of one hook is the input to the next.
type AfterToolHook func(hctx HookContext, toolName string, result agentctx.AgentMessage) (agentctx.AgentMessage, error)

// AfterAgentHook is called once after the agent loop finishes, before AgentEndEvent is pushed.
// Hooks are called sequentially with no data passing between them.
type AfterAgentHook func(hctx HookContext)

// HookRegistry manages registered hooks. All methods are nil-safe:
// calling Run* on a nil HookRegistry returns zero values without panic.
type HookRegistry struct {
	BeforeModelHooks []BeforeModelHook
	AfterToolHooks   []AfterToolHook
	AfterAgentHooks  []AfterAgentHook
}

// RunBeforeModel executes all BeforeModel hooks in fan-out style:
// each hook receives the same messages slice; all non-nil outputs are merged
// and appended to agentCtx.RecentMessages.
// Returns the total number of injected messages.
// If r is nil, returns 0.
func (r *HookRegistry) RunBeforeModel(hctx HookContext, messages []agentctx.AgentMessage) int {
	if r == nil {
		return 0
	}
	var total int
	for i, hook := range r.BeforeModelHooks {
		extra, err := hook(hctx, messages)
		if err != nil {
			slog.Warn("[Hook] BeforeModel hook error",
				"hook_index", i,
				"error", err,
			)
			continue
		}
		if len(extra) > 0 {
			hctx.AgentCtx.RecentMessages = append(hctx.AgentCtx.RecentMessages, extra...)
			total += len(extra)
		}
	}
	return total
}

// RunAfterTool executes all AfterTool hooks in chain style:
// the output of each hook becomes the input of the next.
// Returns the (possibly modified) result.
// If r is nil, returns the input result unchanged.
func (r *HookRegistry) RunAfterTool(hctx HookContext, toolName string, result agentctx.AgentMessage) agentctx.AgentMessage {
	if r == nil {
		return result
	}
	for i, hook := range r.AfterToolHooks {
		modified, err := hook(hctx, toolName, result)
		if err != nil {
			slog.Warn("[Hook] AfterTool hook error",
				"hook_index", i,
				"tool_name", toolName,
				"error", err,
			)
			continue
		}
		result = modified
	}
	return result
}

// RunAfterAgent executes all AfterAgent hooks sequentially.
// If r is nil, this is a no-op.
func (r *HookRegistry) RunAfterAgent(hctx HookContext) {
	if r == nil {
		return
	}
	for _, hook := range r.AfterAgentHooks {
		hook(hctx)
	}
}
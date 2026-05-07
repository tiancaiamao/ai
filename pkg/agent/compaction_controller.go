package agent

import (
	"context"
	"log/slog"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/traceevent"

	compact "github.com/tiancaiamao/ai/pkg/compact"
)

// CompactionDeps holds the dependencies injected from the RPC layer.
// The controller does NOT own stateMu or streaming state — those remain
// in rpc_handlers. Access to state is mediated through callbacks.
type CompactionDeps struct {
	// Compactor performs the actual compaction. May be nil when disabled.
	Compactor *compact.Compactor
	// Agent is the live Agent instance (provides messages + context).
	Agent *Agent
	// EmitEvent emits an event to connected clients (replaces server.EmitEvent).
	EmitEvent func(AgentEvent)
	// SetState sets the isCompacting flag under the caller's mutex.
	SetState func(isCompacting bool)
}

// CompactionController encapsulates compaction decision logic that was
// previously inlined as closures in cmd/ai/rpc_handlers.go.
type CompactionController struct {
	deps CompactionDeps
}

// NewCompactionController creates a new controller with the given dependencies.
func NewCompactionController(deps CompactionDeps) *CompactionController {
	return &CompactionController{deps: deps}
}

// MaybeCompact checks whether compaction is warranted and, if so, runs it.
// The trigger string is used for logging/tracing to identify what caused
// the compaction attempt (e.g. "pre_request_prompt", "pre_request_steer").
func (cc *CompactionController) MaybeCompact(trigger string, sess *session.Session) {
	if cc.deps.Compactor == nil || sess == nil {
		return
	}

	messages := cc.deps.Agent.GetMessages()
	if !cc.deps.Compactor.ShouldCompactOld(messages) {
		return
	}
	if !sess.CanCompact(cc.deps.Compactor) {
		slog.Info("Pre-request compaction skipped: session not compactable",
			"trigger", trigger,
			"messages", len(messages),
			"estimatedTokens", cc.deps.Compactor.EstimateContextTokensOld(messages))
		return
	}

	beforeCount := len(messages)
	compactionInfo := CompactionInfo{
		Auto:    true,
		Before:  beforeCount,
		Trigger: trigger,
	}

	cc.deps.SetState(true)
	cc.deps.EmitEvent(NewCompactionStartEvent(compactionInfo))

	err := cc.runCompactionSpan(sess, trigger, beforeCount, &compactionInfo)

	cc.deps.SetState(false)

	if err != nil {
		compactionInfo.Error = err.Error()
		if session.IsNonActionableCompactionError(err) {
			slog.Info("Pre-request compaction skipped", "trigger", trigger, "reason", err)
		} else {
			slog.Error("Pre-request compaction failed", "trigger", trigger, "error", err)
		}
	}
	cc.deps.EmitEvent(NewCompactionEndEvent(compactionInfo))
}

// runCompactionSpan wraps the compaction work in a trace span.
func (cc *CompactionController) runCompactionSpan(
	sess *session.Session,
	trigger string,
	beforeCount int,
	compactionInfo *CompactionInfo,
) error {
	ctx := context.Background()
	span := traceevent.StartSpan(ctx, "compaction", traceevent.CategoryEvent,
		traceevent.Field{Key: "source", Value: "pre_request"},
		traceevent.Field{Key: "auto", Value: true},
		traceevent.Field{Key: "trigger", Value: trigger},
		traceevent.Field{Key: "before_messages", Value: beforeCount},
	)
	defer span.End()

	result, err := sess.Compact(cc.deps.Compactor)
	if err != nil {
		return err
	}

	cc.deps.Agent.GetContext().RecentMessages = sess.GetMessages()
	afterCount := len(cc.deps.Agent.GetMessages())
	compactionInfo.After = afterCount

	span.AddField("after_messages", afterCount)
	span.AddField("tokens_before", result.TokensBefore)
	span.AddField("tokens_after", result.TokensAfter)
	return nil
}

// RestoreContext restores the llm-context overview.md from the latest
// compaction summary on the current session branch.
func (cc *CompactionController) RestoreContext(sess *session.Session) {
	summary := sess.GetLastCompactionSummary()
	if summary == "" {
		slog.Info("[resume-on-branch] No compaction summary found, skipping llm context restore")
		return
	}

	sessionDir := sess.GetDir()
	if sessionDir == "" {
		slog.Warn("[resume-on-branch] No session directory, cannot restore llm context")
		return
	}

	wm := agentctx.NewLLMContext(sessionDir)
	if err := wm.WriteContent(summary); err != nil {
		slog.Warn("[resume-on-branch] Failed to restore llm context", "error", err)
	} else {
		slog.Info("[resume-on-branch] Restored llm context from compaction summary", "summary_len", len(summary))
	}
}

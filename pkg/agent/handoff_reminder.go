package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// estimateContextTokens returns the total context token count from the last
// assistant message that has valid Usage information. It walks messages
// backward to find the most recent agent-visible assistant message with a
// non-zero token count, skipping messages with aborted/error stop reasons.
// Returns 0 if no message carries usage information.
func estimateContextTokens(messages []agentctx.AgentMessage) int {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !msg.IsAgentVisible() {
			continue
		}
		if msg.Role != "assistant" || msg.Usage == nil {
			continue
		}
		stopReason := strings.ToLower(strings.TrimSpace(msg.StopReason))
		if stopReason == "aborted" || stopReason == "error" {
			continue
		}
		tokens := handoffUsageTotalTokens(msg.Usage)
		if tokens > 0 {
			return tokens
		}
	}
	return 0
}

// handoffUsageTotalTokens extracts the total token count from a Usage struct,
// mirroring the pattern in pkg/compact/compact.go's usageTotalTokens.
func handoffUsageTotalTokens(usage *agentctx.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	return usage.InputTokens + usage.OutputTokens + usage.CacheRead + usage.CacheWrite
}

// handoffThresholds returns the soft and hard token thresholds for handoff
// reminders based on the model's context window size.
//
//   - Windows >= 500000 (1M-class): soft=100000, hard=200000
//   - Smaller windows (200K-class, the default): soft=40000, hard=150000
func handoffThresholds(contextWindow int) (soft, hard int) {
	if contextWindow >= 500000 {
		return 100000, 200000
	}
	return 40000, 150000
}

// injectionInterval calculates the dynamic interval (in tool calls) between
// reminder injections. As token usage approaches the soft threshold, the
// interval shrinks, making reminders more frequent.
//
// The base interval is 10. For each full ratio of currentTokens/softThreshold
// (integer division), the interval decreases by 1, with a minimum of 1.
func injectionInterval(currentTokens, softThreshold int) int {
	const baseInterval = 10
	if softThreshold == 0 {
		return baseInterval
	}
	decay := currentTokens / softThreshold
	n := baseInterval - decay
	if n < 1 {
		return 1
	}
	return n
}

// effectiveContextWindow returns the effective context window, defaulting to
// 200000 when the configured value is 0 or unset.
func effectiveContextWindow(contextWindow int) int {
	if contextWindow > 0 {
		return contextWindow
	}
	return 200000
}

// handoffReminderText formats the standard reminder message body.
func handoffReminderText(tokens, contextWindow, soft, hard int) string {
	return fmt.Sprintf(`<context_management>
Context usage: %d tokens of %d window.
Soft threshold: %d. Hard limit: %d.
Consider performing a context handoff to refresh your context.
Review your progress, key decisions, pending tasks, and current state.
When ready, write the handoff document and end with <handoff_complete>.
</context_management>`, tokens, contextWindow, soft, hard)
}

// urgentHandoffReminderText formats the urgent (hard-limit) reminder message body.
func urgentHandoffReminderText(tokens, contextWindow, soft, hard int) string {
	return fmt.Sprintf(`<context_management>
Context usage: %d tokens of %d window.
Soft threshold: %d. Hard limit: %d.
URGENT: Context usage has reached the hard limit. Handoff is mandatory.
Review your progress, key decisions, pending tasks, and current state.
When ready, write the handoff document and end with <handoff_complete>.
</context_management>`, tokens, contextWindow, soft, hard)
}

// newContextManagementMessage creates a user message with a context_management
// reminder. The message is agent-visible so the LLM sees it.
func newContextManagementMessage(text string) agentctx.AgentMessage {
	msg := agentctx.NewUserMessage(text)
	if msg.Metadata == nil {
		msg.Metadata = &agentctx.MessageMetadata{}
	}
	msg.Metadata.Kind = "context_management"
	msg.Timestamp = time.Now().UnixMilli()
	return msg
}

// maybeInjectHandoffReminder monitors context token usage in handoff mode and
// injects <context_management> reminder messages when thresholds are crossed.
//
// Returns autoExecute=true when the hard threshold has been exceeded for more
// than 2 consecutive turns, signaling that a mandatory handoff should be
// auto-executed.
//
// The method is a no-op when ContextManagementMode != contextModeHandoff.
func (s *loopState) maybeInjectHandoffReminder(ctx context.Context, agentCtx *agentctx.AgentContext) (autoExecute bool) {
	if s.config.ContextManagementMode != contextModeHandoff {
		return false
	}

	currentTokens := estimateContextTokens(agentCtx.RecentMessages)
	soft, hard := handoffThresholds(s.config.ContextWindow)
	window := effectiveContextWindow(s.config.ContextWindow)

	// Below soft threshold — nothing to do, reset hard floor state.
	if currentTokens < soft {
		s.hardFloorCrossed = false
		s.hardFloorTurns = 0
		s.handoffPending = false
		return false
	}

	// At or above soft threshold — interval-based reminder injection.
	if agentCtx.AgentState.ToolCallsSinceLastTrigger >= injectionInterval(currentTokens, soft) {
		reminder := newContextManagementMessage(handoffReminderText(currentTokens, window, soft, hard))
		agentCtx.RecentMessages = append(agentCtx.RecentMessages, reminder)
		s.newMessages = append(s.newMessages, reminder)
		agentCtx.AgentState.ToolCallsSinceLastTrigger = 0
		s.handoffPending = true
		slog.Info("[Loop] Handoff reminder injected (soft threshold)",
			"tokens", currentTokens,
			"soft", soft,
			"hard", hard,
		)
		traceevent.Log(ctx, traceevent.CategoryEvent, "handoff_reminder_injected",
			traceevent.Field{Key: "tokens", Value: currentTokens},
			traceevent.Field{Key: "soft", Value: soft},
			traceevent.Field{Key: "hard", Value: hard},
			traceevent.Field{Key: "urgency", Value: "soft"})
	}

	// At or above hard threshold — urgent reminder every turn.
	if currentTokens >= hard {
		if !s.hardFloorCrossed {
			s.hardFloorCrossed = true
			s.hardFloorTurns = 0
		}
		urgent := newContextManagementMessage(urgentHandoffReminderText(currentTokens, window, soft, hard))
		agentCtx.RecentMessages = append(agentCtx.RecentMessages, urgent)
		s.newMessages = append(s.newMessages, urgent)
		s.hardFloorTurns++
		s.handoffPending = true
		slog.Warn("[Loop] Urgent handoff reminder injected (hard threshold)",
			"tokens", currentTokens,
			"soft", soft,
			"hard", hard,
			"hardFloorTurns", s.hardFloorTurns,
		)
		traceevent.Log(ctx, traceevent.CategoryEvent, "handoff_reminder_injected",
			traceevent.Field{Key: "tokens", Value: currentTokens},
			traceevent.Field{Key: "soft", Value: soft},
			traceevent.Field{Key: "hard", Value: hard},
			traceevent.Field{Key: "urgency", Value: "hard"})
		if s.hardFloorTurns > 2 {
			traceevent.Log(ctx, traceevent.CategoryEvent, "handoff_auto_execute_triggered",
				traceevent.Field{Key: "tokens", Value: currentTokens},
				traceevent.Field{Key: "hard", Value: hard})
			return true
		}
	} else {
		// Below hard threshold — reset hard floor tracking.
		s.hardFloorCrossed = false
		s.hardFloorTurns = 0
	}

	return false
}

package agent

import (
	"context"
	"fmt"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
	"log/slog"
)

// toolLoopGuard detects and prevents infinite tool call loops.
// It only counts consecutive calls with the same signature (name + arguments hash).
// When the tool or arguments change, the counter resets to 1.
type toolLoopGuard struct {
	maxConsecutive int

	lastSignature  string
	consecutiveRun int
}

func newToolLoopGuard(config *LoopConfig) *toolLoopGuard {
	if config == nil {
		return nil
	}
	maxConsecutive := resolveLoopGuardLimit(config.MaxConsecutiveToolCalls, defaultLoopMaxConsecutiveToolCalls)
	if maxConsecutive == 0 {
		return nil
	}
	return &toolLoopGuard{
		maxConsecutive: maxConsecutive,
	}
}

func resolveLoopGuardLimit(value, defaultValue int) int {
	if value < 0 {
		return 0
	}
	if value == 0 {
		return defaultValue
	}
	return value
}

// Observe checks tool calls for loop patterns.
// It only counts consecutive calls with the same signature (name + arguments hash).
// When the signature changes, the counter resets.
func (g *toolLoopGuard) Observe(toolCalls []agentctx.ToolCallContent) (bool, string) {
	for _, tc := range toolCalls {
		name := strings.ToLower(strings.TrimSpace(tc.Name))
		if name == "" {
			name = "unknown"
		}
		signature := name + ":" + hashAny(tc.Arguments)

		if signature == g.lastSignature {
			g.consecutiveRun++
		} else {
			g.lastSignature = signature
			g.consecutiveRun = 1
		}

		if g.maxConsecutive > 0 && g.consecutiveRun > g.maxConsecutive {
			return true, fmt.Sprintf("detected %d consecutive identical tool calls (%s)", g.consecutiveRun, name)
		}
	}
	return false, ""
}

func sanitizeMessageForToolLoopGuard(msg *agentctx.AgentMessage, reason string) {
	if msg == nil {
		return
	}

	filtered := make([]agentctx.ContentBlock, 0, len(msg.Content)+1)
	for _, block := range msg.Content {
		switch block.(type) {
		case agentctx.ToolCallContent:
		default:
			filtered = append(filtered, block)
		}
	}
	filtered = append(filtered, agentctx.TextContent{
		Type: "text",
		Text: "\n\n[Loop guard] Stopped repeated tool execution to prevent an infinite loop.\nReason: " + strings.TrimSpace(reason),
	})
	msg.Content = filtered
	msg.StopReason = "aborted"
}

// isSuccessfulStopReason returns true if the stopReason indicates a successful completion.
// Successful stopReason values are: "stop", "tool_calls", "toolUse", "length".
// Any other value indicates an error or abnormal termination that should be reported to the user.
func isSuccessfulStopReason(stopReason string) bool {
	switch stopReason {
	case "stop", "tool_calls", "toolUse", "length":
		// "stop" - normal completion
		// "tool_calls"/"toolUse" - LLM wants to use tools
		// "length" - hit max token limit (still completed normally)
		return true
	default:
		// Empty string or any other value is not a successful stop reason.
		// An empty stopReason means the LLM response was truncated or incomplete.
		return false
	}
}

// sanitizeMessageForNonSuccessStopReason modifies the message to notify the user
// about any non-success stopReason. This ensures the user is informed instead of
// experiencing a silent failure for network errors, rate limits, timeouts, etc.
//
// Returns true if the message was sanitized (stopReason was non-success), false otherwise.
func sanitizeMessageForNonSuccessStopReason(msg *agentctx.AgentMessage) bool {
	if msg == nil {
		return false
	}

	stopReason := msg.StopReason
	if isSuccessfulStopReason(stopReason) {
		return false
	}

	// Filter out tool calls since the request failed before they could be executed
	filtered := make([]agentctx.ContentBlock, 0, len(msg.Content)+1)
	for _, block := range msg.Content {
		switch block.(type) {
		case agentctx.ToolCallContent:
			// Remove tool calls since they failed due to the error
		default:
			filtered = append(filtered, block)
		}
	}

	// Generate user-facing error message based on stopReason
	var errorMsg string
	switch stopReason {
	case "network_error":
		errorMsg = "[Network error] The request failed due to a network issue. Please check your connection and try again."
	case "rate_limit_error", "rate_limit":
		errorMsg = "[Rate limit] The request was rate-limited. Please wait a moment and try again."
	case "timeout":
		errorMsg = "[Timeout] The request timed out. Please try again."
	case "error":
		errorMsg = "[Error] The request failed. Please try again."
	default:
		// Handle any other unexpected stopReason
		errorMsg = fmt.Sprintf("[Request failed] The request ended unexpectedly: %s. Please try again.", stopReason)
	}

	filtered = append(filtered, agentctx.TextContent{
		Type: "text",
		Text: "\n\n" + errorMsg,
	})
	msg.Content = filtered
	// Keep the original stopReason for proper categorization
	return true
}

func maybeRecoverMalformedToolCall(
	ctx context.Context,
	agentCtx *agentctx.AgentContext,
	newMessages *[]agentctx.AgentMessage,
	stream *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	msg *agentctx.AgentMessage,
	recoveryCount *int,
) bool {
	if msg == nil || agentCtx == nil || recoveryCount == nil {
		return false
	}
	shouldRecover, reason := shouldRecoverMalformedToolCall(msg)
	if !shouldRecover {
		return false
	}
	if *recoveryCount >= defaultMalformedToolCallRecoveries {
		slog.Warn("[Loop] malformed tool-call recovery limit reached",
			"recoveryCount", *recoveryCount,
			"reason", reason)
		return false
	}

	*recoveryCount = *recoveryCount + 1
	recoveryMsg := buildMalformedToolCallRecoveryMessage(reason, *recoveryCount)
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, recoveryMsg)
	if newMessages != nil {
		*newMessages = append(*newMessages, recoveryMsg)
	}
	if stream != nil {
		stream.Push(NewToolCallRecoveryEvent(ToolCallRecoveryInfo{
			Reason:  reason,
			Attempt: *recoveryCount,
		}))
	}
	traceevent.Log(ctx, traceevent.CategoryTool, "malformed_tool_call_recovery",
		traceevent.Field{Key: "attempt", Value: *recoveryCount},
		traceevent.Field{Key: "reason", Value: reason},
	)
	slog.Warn("[Loop] malformed tool call recovered",
		"attempt", *recoveryCount,
		"reason", reason)
	return true
}

func shouldRecoverMalformedToolCall(msg *agentctx.AgentMessage) (bool, string) {
	if msg == nil || len(msg.ExtractToolCalls()) > 0 {
		return false, ""
	}

	if msg.StopReason == "tool_calls" {
		return true, "stop_reason=tool_calls but no parsable tool call was produced"
	}

	text := strings.TrimSpace(msg.ExtractText())
	thinking := strings.TrimSpace(msg.ExtractThinking())

	candidates := []struct {
		source string
		text   string
	}{
		{source: "text", text: text},
		{source: "thinking", text: thinking},
	}

	for _, candidate := range candidates {
		body := strings.TrimSpace(candidate.text)
		if body == "" {
			continue
		}

		issues := DetectIncompleteToolCalls(body)
		if len(issues) > 0 {
			return true, fmt.Sprintf("%s: %s", candidate.source, strings.Join(issues, "; "))
		}

		lower := strings.ToLower(body)
		if strings.Contains(lower, "<tool_call") ||
			strings.Contains(lower, "<tool>") ||
			strings.Contains(lower, "ErrorException") ||
			strings.Contains(lower, " excer ") {
			return true, fmt.Sprintf("%s: detected tool-call markup without a valid parsed tool call", candidate.source)
		}
	}

	return false, ""
}

func buildMalformedToolCallRecoveryMessage(reason string, attempt int) agentctx.AgentMessage {
	cleanReason := strings.TrimSpace(reason)
	if cleanReason == "" {
		cleanReason = "unknown parse failure"
	}

	text := fmt.Sprintf(
		"[agentctx.Tool-call recovery, attempt %d] Your previous response attempted a tool invocation but the tool call format was invalid (%s). Re-emit the intended call using valid tool/function-call syntax only. If no tool is needed, provide the final answer directly.",
		attempt,
		truncateLine(cleanReason, 220),
	)
	return agentctx.NewUserMessage(text).WithVisibility(true, false).WithKind("tool_call_repair")
}
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
// It guards against two loop shapes:
//
//  1. Consecutive calls with the same signature (name + arguments hash);
//  2. Short repeated patterns that never repeat consecutively, e.g.
//     A,B,A,B,A,B (period 2) or A,B,C,A,B,C,A (period 3), where every
//     neighbouring pair differs and the consecutive counter alone would reset.
//
// The guard also tracks tool output changes: if the output keeps changing despite
// identical tool calls, this indicates legitimate polling rather than a stuck loop.
// In that case the guard softens its response - soft feedback is still issued to
// encourage the LLM to change approach, but hard abort is suppressed until the
// output stops changing. Polling suppression applies only to identical tool
// calls (the consecutive path); the pattern path never compares outputs of
// different calls against each other.
//
// Strategy: instead of aborting immediately, the guard returns a ToolResult
// with actionable feedback so the LLM can self-correct. After maxFeedbackAttempts
// the guard escalates to a hard abort (unless output is still changing).
type toolLoopGuard struct {
	maxConsecutive int

	lastSignature  string
	consecutiveRun int

	// Pattern detection state for short repeated patterns (period 2 or 3)
	// that the consecutive-identical counter misses.
	patternHistory []string
	patternPeriod  int
	patternRun     int

	// feedbackCount tracks how many times the guard has returned feedback
	// to the LLM for the same repeated signature or pattern.
	feedbackCount int
	// maxFeedbackAttempts is the number of feedback rounds before hard abort.
	// 0 means use the default (defaultLoopGuardMaxFeedback).
	maxFeedbackAttempts int

	// lastOutputHash is a hash of the last tool output for the current signature.
	// Used to detect whether a repeated tool call is producing different results
	// (legitimate polling) vs. identical results (stuck loop).
	lastOutputHash string
	// outputChangedSinceBlock is true when tool output has changed since the
	// guard first started blocking this signature. When true, hard abort is
	// suppressed - the guard only issues soft feedback.
	// Only meaningful for the consecutive-identical path: the pattern path
	// resets this state on every call so outputs of different calls are never
	// compared against each other.
	outputChangedSinceBlock bool
}

const defaultLoopGuardMaxFeedback = 2

// defaultLoopMaxPatternRepeats is how many pattern observations the guard
// tolerates before blocking a short repeated pattern. It is smaller than the
// consecutive-identical threshold because an alternating loop (A,B,A,B,...)
// is just as wasteful but never trips the consecutive counter.
const defaultLoopMaxPatternRepeats = 4

// maxPatternHistory caps the rolling signature window. Long enough for
// period-2 and period-3 detection.
const maxPatternHistory = 6

func newToolLoopGuard(config *LoopConfig) *toolLoopGuard {
	if config == nil {
		return nil
	}
	maxConsecutive := resolveLoopGuardLimit(config.MaxConsecutiveToolCalls, defaultLoopMaxConsecutiveToolCalls)
	if maxConsecutive == 0 {
		return nil
	}
	maxFeedback := defaultLoopGuardMaxFeedback
	if config.MaxLoopGuardFeedback > 0 {
		maxFeedback = config.MaxLoopGuardFeedback
	}
	return &toolLoopGuard{
		maxConsecutive:      maxConsecutive,
		maxFeedbackAttempts: maxFeedback,
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

// ObserveResult holds the outcome of a loop guard check.
type ObserveResult struct {
	// Blocked is true when a loop is detected.
	Blocked bool
	// Reason describes why the loop was detected.
	Reason string
	// HardAbort is true when the guard has exhausted feedback attempts
	// and the loop must be terminated.
	HardAbort bool
	// FeedbackAttempt is the 1-based feedback round number (0 if not feedback).
	FeedbackAttempt int
}

// NotifyToolOutput records the output of a tool execution so the guard can
// detect polling patterns (same tool+args but changing output).
// Call this after tool execution completes with a hash of the output content.
func (g *toolLoopGuard) NotifyToolOutput(outputHash string) {
	if g.lastOutputHash != "" && g.lastOutputHash != outputHash {
		g.outputChangedSinceBlock = true
	}
	g.lastOutputHash = outputHash
}

// Observe checks tool calls for loop patterns.
// It detects both consecutive calls with the same signature and short repeated
// patterns (period 2 or 3) such as A,B,A,B or A,B,C,A,B,C that never repeat
// consecutively.
//
// If tool output has been changing (detected via NotifyToolOutput), the guard
// suppresses hard abort and only issues soft feedback - the LLM may be
// legitimately polling for progress.
//
// Returns an ObserveResult indicating whether a loop was detected and whether
// the guard should return feedback to the LLM (soft) or abort (hard).
func (g *toolLoopGuard) Observe(toolCalls []agentctx.ToolCallContent) ObserveResult {
	for _, tc := range toolCalls {
		name := strings.ToLower(strings.TrimSpace(tc.Name))
		if name == "" {
			name = "unknown"
		}
		signature := name + ":" + hashAny(tc.Arguments)

		consecutive := signature == g.lastSignature
		if consecutive {
			g.consecutiveRun++
			// A consecutive-identical call cannot be part of an alternating
			// period-2/3 pattern. Invalidate any active pattern candidate so
			// stale state cannot false-trigger later (e.g. A,B,A,A,B,A), and
			// keep the window faithful to the actual call sequence.
			g.patternPeriod = 0
			g.patternRun = 0
			g.recordSignature(signature)
		} else {
			g.lastSignature = signature
			g.consecutiveRun = 1

			// Short repeated-pattern tracking. Only evaluated when the
			// signature changed: pure consecutive runs are already covered
			// by the consecutive-identical counter.
			if g.observePattern(signature) {
				// The pattern continues: preserve the feedback escalation but
				// reset output-polling state. Different calls within a pattern
				// are not identical tool calls, so their outputs must not be
				// compared against each other to infer polling.
				g.lastOutputHash = ""
				g.outputChangedSinceBlock = false
			} else {
				// A new pattern or a break: reset all state, so a genuine
				// change of approach starts fresh.
				g.feedbackCount = 0
				g.lastOutputHash = ""
				g.outputChangedSinceBlock = false
			}
		}

		if result, triggered := g.loopTrigger(name); triggered {
			return result
		}
	}
	return ObserveResult{Blocked: false}
}

// recordSignature appends a signature to the rolling window, keeping it capped.
func (g *toolLoopGuard) recordSignature(signature string) {
	g.patternHistory = append(g.patternHistory, signature)
	if len(g.patternHistory) > maxPatternHistory {
		g.patternHistory = g.patternHistory[len(g.patternHistory)-maxPatternHistory:]
	}
}

// observePattern updates the short repeated-pattern state for a non-consecutive
// signature. It reports whether the call continues an already-active period-2/3
// pattern (preserving feedback escalation so the guard escalates to hard abort
// within one continuous pattern) rather than starting a new pattern or breaking
// the current one.
func (g *toolLoopGuard) observePattern(signature string) bool {
	g.recordSignature(signature)

	// The signature is at the tail; the call p positions back is at index
	// len-1-p, so period-2 matches index len-3 and period-3 index len-4.
	period := 0
	if len(g.patternHistory) >= 3 && g.patternHistory[len(g.patternHistory)-3] == signature {
		period = 2
	} else if len(g.patternHistory) >= 4 && g.patternHistory[len(g.patternHistory)-4] == signature {
		period = 3
	}

	if period == 0 {
		// No periodic alignment - the short pattern, if any, is broken.
		g.patternPeriod = 0
		g.patternRun = 0
		return false
	}
	if period == g.patternPeriod {
		// Continuing the active pattern.
		g.patternRun++
		return true
	}
	// A pattern became observable: it takes at least two observations for a
	// period-2/3 cycle to show up (e.g. A,B,A for period 2).
	g.patternPeriod = period
	g.patternRun = 2
	return false
}

// loopTrigger reports whether either loop counter (consecutive-identical or
// short repeated pattern) exceeds its threshold and, when it does, builds the
// shared block result. Feedback escalation and polling suppression are handled
// here so both detection mechanisms behave identically.
func (g *toolLoopGuard) loopTrigger(name string) (ObserveResult, bool) {
	var reason string
	var triggered bool

	if g.maxConsecutive > 0 && g.consecutiveRun > g.maxConsecutive {
		triggered = true
		reason = fmt.Sprintf("detected %d consecutive identical tool calls (%s)", g.consecutiveRun, name)
	}
	if g.patternRun > defaultLoopMaxPatternRepeats {
		if triggered {
			reason += "; "
		}
		reason += fmt.Sprintf("detected repeated tool-call pattern with period %d (%s)", g.patternPeriod, name)
		triggered = true
	}
	if !triggered {
		return ObserveResult{}, false
	}

	if g.outputChangedSinceBlock {
		reason += " (output is changing - likely polling, not stuck)"
	}

	// Check if we've exhausted feedback attempts.
	// Suppress hard abort if output is still changing (legitimate polling).
	if g.feedbackCount >= g.maxFeedbackAttempts && !g.outputChangedSinceBlock {
		return ObserveResult{
			Blocked:   true,
			Reason:    reason,
			HardAbort: true,
		}, true
	}

	g.feedbackCount++
	return ObserveResult{
		Blocked:         true,
		Reason:          reason,
		HardAbort:       false,
		FeedbackAttempt: g.feedbackCount,
	}, true
}

// buildLoopGuardToolResults creates ToolResult messages for each tool call that
// was blocked by the loop guard. These results are returned to the LLM as
// feedback so it can self-correct its approach.
func buildLoopGuardToolResults(toolCalls []agentctx.ToolCallContent, result ObserveResult, maxFeedback int) []agentctx.AgentMessage {
	results := make([]agentctx.AgentMessage, 0, len(toolCalls))
	feedbackMsg := fmt.Sprintf(
		"[Loop guard] Repeated identical tool call detected: %s\n\n"+
			"You have made the same tool call with identical arguments multiple times in a row. "+
			"This suggests the tool is not producing the expected result or you are stuck in a loop.\n\n"+
			"Please try a different approach:\n"+
			"- Use different arguments or parameters\n"+
			"- Try a different tool entirely\n"+
			"- If the tool keeps returning an error, investigate the root cause first\n"+
			"- Consider whether you need this tool call at all\n\n"+
			"Feedback attempt %d of %d. If you continue with the same call, the loop will be terminated.",
		strings.TrimSpace(result.Reason),
		result.FeedbackAttempt,
		maxFeedback,
	)

	for _, tc := range toolCalls {
		name := strings.ToLower(strings.TrimSpace(tc.Name))
		if name == "" {
			name = "unknown"
		}
		toolResult := agentctx.NewToolResultMessage(tc.ID, name, []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: feedbackMsg},
		}, true)
		results = append(results, toolResult)
	}
	return results
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

package compact

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/tools/context_mgmt"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// Trigger thresholds for context management.
const (
	MgmtTokenLow    = 0.20 // 20%: start periodic checks
	MgmtTokenMedium = 0.33 // 30%: more aggressive checks
	MgmtTokenHigh   = 0.50 // 50%: frequent checks

	MgmtIntervalLow    = 15 // At 20%: every 15 tool calls
	MgmtIntervalMedium = 10 // At 40%: every 10 tool calls
	MgmtIntervalHigh   = 7  // At 60%: every 7 tool calls

	// Tool output preview limits for context management messages
	// These should match TruncateWithHeadTail constants to ensure
	// the LLM sees an accurate preview of what truncation will preserve
	mgmtPreviewMax  = 2500 // Increased to match TruncateWithHeadTail (1000+1000+500)
	mgmtPreviewHead = 1000 // Must match TruncateWithHeadTail headKeep
	mgmtPreviewTail = 1000 // Must match TruncateWithHeadTail tailKeep

	// When estimated truncation savings is above this threshold, context management
	// should prioritize truncate_messages before update_llm_context.
	// Reduced from 5000 to 2000 to be more aggressive about truncation.
	mgmtForceTruncateSavingsTokens = 2000

	// If there are any messages with char count above this threshold,
	// the compactor should consider truncating them even if total savings is low.
	mgmtLargeMessageThreshold = 2000 // Reduced from 3000 to be more aggressive
)

// ContextManagerConfig holds configuration.
type ContextManagerConfig struct {
	TokenLow    float64
	TokenMedium float64
	TokenHigh   float64

	IntervalLow    int
	IntervalMedium int
	IntervalHigh   int

	AutoCompact bool
}

// DefaultContextManagerConfig returns defaults.
func DefaultContextManagerConfig() *ContextManagerConfig {
	return &ContextManagerConfig{
		TokenLow:    MgmtTokenLow,
		TokenMedium: MgmtTokenMedium,
		TokenHigh:   MgmtTokenHigh,

		IntervalLow:    MgmtIntervalLow,
		IntervalMedium: MgmtIntervalMedium,
		IntervalHigh:   MgmtIntervalHigh,

		AutoCompact: true,
	}
}

// ContextManager performs lightweight LLM-driven context management.
// It is triggered periodically by the agent loop and makes an independent LLM
// call with context-management-specific tools (truncate_messages, update_llm_context, compact, no_action).
// The main LLM is never involved in context management decisions.
type ContextManager struct {
	config        *ContextManagerConfig
	model         llm.Model
	apiKey        string
	contextWindow int
	systemPrompt  string
	compactor     *Compactor // Optional: full compactor for compact tool
}

// NewContextManager creates a new ContextManager.
func NewContextManager(
	config *ContextManagerConfig,
	model llm.Model,
	apiKey string,
	contextWindow int,
	systemPrompt string,
	compactor *Compactor,
) *ContextManager {
	if config == nil {
		config = DefaultContextManagerConfig()
	}
	return &ContextManager{
		config:        config,
		model:         model,
		apiKey:        apiKey,
		contextWindow: contextWindow,
		systemPrompt:  systemPrompt,
		compactor:     compactor,
	}
}

// SetCompactor sets the full compactor for compact tool support.
func (c *ContextManager) SetCompactor(compactor *Compactor) {
	c.compactor = compactor
}

// ShouldCompact checks if the compactor should run.
// It uses token percentage and tool-call interval to decide.
func (c *ContextManager) ShouldCompact(ctx context.Context, agentCtx *agentctx.AgentContext) bool {
	if !c.config.AutoCompact {
		traceevent.Log(ctx, traceevent.CategoryEvent, "context_mgmt_check",
			traceevent.Field{Key: "decision", Value: false},
			traceevent.Field{Key: "reason", Value: "auto_compact_disabled"},
		)
		return false
	}

	tokenPercent := c.estimateTokenPercent(agentCtx)
	if tokenPercent < c.config.TokenLow {
		traceevent.Log(ctx, traceevent.CategoryEvent, "context_mgmt_check",
			traceevent.Field{Key: "decision", Value: false},
			traceevent.Field{Key: "reason", Value: "below_token_low"},
			traceevent.Field{Key: "token_percent", Value: fmt.Sprintf("%.1f%%", tokenPercent*100)},
			traceevent.Field{Key: "threshold_low", Value: fmt.Sprintf("%.0f%%", c.config.TokenLow*100)},
		)
		return false
	}

	toolCallsSince := agentCtx.AgentState.ToolCallsSinceLastTrigger
	var interval int
	var tier string
	switch {
	case tokenPercent >= c.config.TokenHigh:
		interval = c.config.IntervalHigh
		tier = "high"
	case tokenPercent >= c.config.TokenMedium:
		interval = c.config.IntervalMedium
		tier = "medium"
	default:
		interval = c.config.IntervalLow
		tier = "low"
	}

	shouldCompact := toolCallsSince >= interval
	traceevent.Log(ctx, traceevent.CategoryEvent, "context_mgmt_check",
		traceevent.Field{Key: "decision", Value: shouldCompact},
		traceevent.Field{Key: "token_percent", Value: fmt.Sprintf("%.1f%%", tokenPercent*100)},
		traceevent.Field{Key: "tier", Value: tier},
		traceevent.Field{Key: "tool_calls_since", Value: toolCallsSince},
		traceevent.Field{Key: "interval", Value: interval},
		traceevent.Field{Key: "reason", Value: func() string {
			if shouldCompact {
				return "threshold_met"
			}
			return "interval_not_reached"
		}()},
	)
	return shouldCompact
}

// Compact runs the LLM-driven context management cycle.
// Uses context.Background() internally. For context-aware cancellation,
// use CompactWithCtx instead.
func (c *ContextManager) Compact(agentCtx *agentctx.AgentContext) (*agentctx.CompactionResult, error) {
	return c.CompactWithCtx(context.Background(), agentCtx)
}

// CompactWithCtx runs the LLM-driven context management cycle with context support.
func (c *ContextManager) CompactWithCtx(parent context.Context, agentCtx *agentctx.AgentContext) (*agentctx.CompactionResult, error) {
	span := traceevent.StartSpan(parent, "context_mgmt", traceevent.CategoryEvent,
		traceevent.Field{Key: "messages_before", Value: len(agentCtx.RecentMessages)},
		traceevent.Field{Key: "token_pct", Value: fmt.Sprintf("%.1f%%", c.estimateTokenPercent(agentCtx)*100)},
		traceevent.Field{Key: "tool_calls_since", Value: agentCtx.AgentState.ToolCallsSinceLastTrigger},
	)
	defer span.End()

	start := time.Now()
	tokensBefore := agentCtx.EstimateTokens()

	slog.Info("[CtxMgmt] Starting compact",
		"messages", len(agentCtx.RecentMessages),
		"token_pct", fmt.Sprintf("%.1f%%", c.estimateTokenPercent(agentCtx)*100),
	)

	// 1. Build context management messages (full conversation with annotations)
	messages := c.buildContextMgmtMessages(agentCtx)

	// 2. Get context management tools
	var tools []context_mgmt.Tool
	if c.compactor != nil {
		tools = context_mgmt.GetContextManagementTools(agentCtx)
		// Add compact tool manually to avoid circular import
		tools = append(tools, NewCompactTool(agentCtx, c.compactor))
	} else {
		tools = context_mgmt.GetContextManagementTools(agentCtx)
	}

	// 3. Call LLM
	llmMessages := append([]llm.LLMMessage{{
		Role:    "system",
		Content: c.systemPrompt,
	}}, messages...)

	stream := llm.StreamLLM(
		parent,
		c.model,
		llm.LLMContext{
			Messages: llmMessages,
			Tools:    c.convertToolsToLLM(tools),
		},
		c.apiKey,
		2*time.Minute,
	)

	// 4. Extract tool calls from stream
	toolCalls, err := c.extractToolCalls(parent, stream)
	if err != nil {
		slog.Error("[CtxMgmt] LLM call failed", "error", err)
		return nil, fmt.Errorf("context management LLM call failed: %w", err)
	}

	// 5. Execute tool calls and track results
	truncatedCount, llmContextUpdated := c.executeToolCalls(parent, toolCalls, tools)

	// 6. Validate that required tool pairings were followed
	if truncatedCount > 0 && !llmContextUpdated {
		slog.Warn("[CtxMgmt] TRUNCATE WITHOUT UPDATE: truncate_messages was called but update_llm_context was not - this breaks task continuity",
			"truncated_count", truncatedCount,
		)
		// Add a trace event to highlight this failure
		traceevent.Log(parent, traceevent.CategoryEvent, "context_mgmt_validation_failed",
			traceevent.Field{Key: "reason", Value: "truncate_without_update"},
			traceevent.Field{Key: "truncated_count", Value: truncatedCount},
		)
	} else if !llmContextUpdated && agentCtx.LLMContext == "" {
		slog.Error("[CtxMgmt] EMPTY LLM CONTEXT: context management ran without updating LLM Context - agent cannot continue",
			"tool_calls", len(toolCalls),
		)
		// Add a trace event to highlight this failure
		traceevent.Log(parent, traceevent.CategoryEvent, "context_mgmt_validation_failed",
			traceevent.Field{Key: "reason", Value: "empty_llm_context"},
			traceevent.Field{Key: "tool_calls", Value: len(toolCalls)},
		)
	}

	// 7. Reset trigger counters
	agentCtx.AgentState.LastTriggerTurn = agentCtx.AgentState.TotalTurns
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 0
	agentCtx.AgentState.UpdatedAt = time.Now()

	tokensAfter := agentCtx.EstimateTokens()
	duration := time.Since(start)

	span.AddField("tokens_before", tokensBefore)
	span.AddField("tokens_after", tokensAfter)
	span.AddField("tokens_saved", tokensBefore-tokensAfter)
	span.AddField("messages_after", len(agentCtx.RecentMessages))
	span.AddField("tool_calls", len(toolCalls))
	span.AddField("truncated_count", truncatedCount)
	span.AddField("llm_context_updated", llmContextUpdated)

	slog.Info("[CtxMgmt] Compact complete",
		"tokens_before", tokensBefore,
		"tokens_after", tokensAfter,
		"saved", tokensBefore-tokensAfter,
		"tool_calls", len(toolCalls),
		"truncated", truncatedCount,
		"llm_context_updated", llmContextUpdated,
		"duration", duration,
	)

	return &agentctx.CompactionResult{
		Summary:           fmt.Sprintf("LLM context management: %d tool calls executed", len(toolCalls)),
		TokensBefore:      tokensBefore,
		TokensAfter:       tokensAfter,
		Type:              "mini",
		TruncatedCount:    truncatedCount,
		LLMContextUpdated: llmContextUpdated,
	}, nil
}

// CalculateDynamicThreshold returns the token threshold for compaction.
func (c *ContextManager) CalculateDynamicThreshold() int {
	if c.contextWindow <= 0 {
		return 0
	}
	return int(float64(c.contextWindow) * c.config.TokenLow)
}

// --- Internal helpers ---

func (c *ContextManager) estimateTokenPercent(ctx *agentctx.AgentContext) float64 {
	if c.contextWindow <= 0 {
		return 0
	}
	return float64(ctx.EstimateTokens()) / float64(c.contextWindow)
}

type truncationCandidate struct {
	ID           string
	ToolName     string
	Chars        int
	SavingsToken int
}

func collectTruncationCandidates(agentCtx *agentctx.AgentContext, protectedStart int) ([]truncationCandidate, int, int) {
	if protectedStart > len(agentCtx.RecentMessages) {
		protectedStart = len(agentCtx.RecentMessages)
	}

	candidates := make([]truncationCandidate, 0)
	truncatedCount := 0
	nonSelectableCount := 0

	for i := 0; i < protectedStart; i++ {
		msg := agentCtx.RecentMessages[i]
		if msg.Role != "toolResult" {
			continue
		}
		if msg.Truncated {
			truncatedCount++
			continue
		}

		id := strings.TrimSpace(msg.ToolCallID)
		if id == "" {
			nonSelectableCount++
			continue
		}

		text := msg.ExtractText()
		// Skip very small outputs - not worth truncating
		if len(text) < 500 {
			nonSelectableCount++
			continue
		}

		truncatedText := agentctx.TruncateWithHeadTail(text)
		savedChars := len(text) - len(truncatedText)
		if savedChars < 0 {
			savedChars = 0
		}

		candidates = append(candidates, truncationCandidate{
			ID:           id,
			ToolName:     msg.ToolName,
			Chars:        len(text),
			SavingsToken: savedChars / 4,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].SavingsToken == candidates[j].SavingsToken {
			return candidates[i].Chars > candidates[j].Chars
		}
		return candidates[i].SavingsToken > candidates[j].SavingsToken
	})

	return candidates, truncatedCount, nonSelectableCount
}

// buildContextMgmtMessages builds the message sequence for context management.
// Sends the FULL conversation with annotations so the LLM can judge
// whether each tool output is still useful.
func (c *ContextManager) buildContextMgmtMessages(agentCtx *agentctx.AgentContext) []llm.LLMMessage {
	protectedStart := len(agentCtx.RecentMessages) - agentctx.RecentMessagesKeep
	if protectedStart < 0 {
		protectedStart = 0
	}

	candidates, truncatedCount, nonSelectableCount := collectTruncationCandidates(agentCtx, protectedStart)
	truncatableCount := len(candidates)
	estimatedSavingsTokens := 0
	largeMessageCount := 0
	for _, candidate := range candidates {
		estimatedSavingsTokens += candidate.SavingsToken
		if candidate.Chars > mgmtLargeMessageThreshold {
			largeMessageCount++
		}
	}

	// Build conversation as a single user message with annotations
	var conv strings.Builder

	// LLM context first (if exists)
	if agentCtx.LLMContext != "" {
		conv.WriteString("## Current LLM Context\n")
		conv.WriteString(agentCtx.LLMContext)
		conv.WriteString("\n\n")
	}

	conv.WriteString("## Conversation History\n\n")

	for msgIdx, msg := range agentCtx.RecentMessages {
		if !msg.IsAgentVisible() {
			continue
		}
		if msg.Truncated {
			conv.WriteString(fmt.Sprintf("[%s] (already truncated)\n%s\n\n", msg.Role, msg.ExtractText()))
			continue
		}

		switch msg.Role {
		case "user":
			conv.WriteString("[user]\n")
			conv.WriteString(msg.ExtractText())
			conv.WriteString("\n\n")
		case "assistant":
			content := msg.ExtractText()
			toolCalls := msg.ExtractToolCalls()
			if len(toolCalls) > 0 {
				conv.WriteString("[assistant] (tool calls)\n")
				for _, tc := range toolCalls {
					conv.WriteString(fmt.Sprintf("  -> %s(%s)\n", tc.Name, compactArgsStr(tc.Arguments)))
				}
			} else if content != "" {
				conv.WriteString("[assistant]\n")
				conv.WriteString(content)
			}
			conv.WriteString("\n\n")
		case "toolResult":
			content := msg.ExtractText()
			if len(content) > mgmtPreviewMax {
				head := content[:mgmtPreviewHead]
				tail := content[len(content)-mgmtPreviewTail:]
				omitted := len(content) - mgmtPreviewHead - mgmtPreviewTail
				content = fmt.Sprintf("%s\n... (%d chars omitted) ...\n%s", head, omitted, tail)
			}
			if msgIdx >= protectedStart {
				// Protected: show content but hide ID so LLM can't select it
				conv.WriteString(fmt.Sprintf("[tool:%s chars=%d PROTECTED]\n%s\n\n",
					msg.ToolName, len(msg.ExtractText()), content))
			} else if strings.TrimSpace(msg.ToolCallID) == "" {
				// Older events may not carry tool_call_id and cannot be targeted by truncate_messages.
				conv.WriteString(fmt.Sprintf("[tool:%s chars=%d NON_TRUNCATABLE:NO_ID]\n%s\n\n",
					msg.ToolName, len(msg.ExtractText()), content))
			} else {
				conv.WriteString(fmt.Sprintf("[tool:%s chars=%d] id=%s\n%s\n\n",
					msg.ToolName, len(msg.ExtractText()), msg.ToolCallID, content))
			}
		}
	}

	messages := []llm.LLMMessage{{
		Role:    "user",
		Content: conv.String(),
	}}

	// Extract latest user request for task grounding
	latestUserRequest := extractLatestUserRequest(agentCtx.RecentMessages)

	// State message as the final user message
	tokenPercent := c.estimateTokenPercent(agentCtx)
	recommendedTruncate := estimatedSavingsTokens >= mgmtForceTruncateSavingsTokens
	llmContextEmpty := agentCtx.LLMContext == ""
	
	stateMsg := fmt.Sprintf(`<current_state>
Truncatable tool outputs (selectable): %d (protected region: last %d messages)
Estimated savings if truncating selectable outputs: ~%d tokens
Large outputs (>2000 chars): %d
Non-truncatable old tool outputs (missing ID or too small): %d
Already truncated outputs: %d
Tokens used: %.1f%%
Tool calls since last management: %d
Total turns: %d
Total truncations so far: %d
Total compactions so far: %d
Last compact turn: %d
Current LLM Context exists: %t
</current_state>

<latest_user_request>
%s
</latest_user_request>

Review the conversation above and decide the best action.

Decision rules:
1. If truncatable output count is 0, do NOT call truncate_messages.
2. If estimated savings is >= %d tokens, you MUST call truncate_messages first, then update_llm_context.
3. If large outputs (%d) exist, consider truncating them even if total savings is modest.
4. ALWAYS prefer truncate over no_action when large old outputs are present.
5. When you truncate, you MUST also call update_llm_context to preserve key information from truncated outputs.
6. Your update_llm_context MUST reflect the task shown in <latest_user_request> — do NOT fabricate a different task. ALWAYS include the latest user request verbatim in your LLM Context.
7. ⚠️ CRITICAL: If Current LLM Context exists is false, you MUST call update_llm_context even if you don't truncate. The agent cannot continue without a valid LLM Context.
8. DO NOT truncate outputs that contain content needed for <latest_user_request> — check each ID against the task first.
9. DO NOT truncate small outputs (<500 chars) — negligible savings, high risk of losing critical details.
10. Your top priority is TASK CONTINUITY — ensure the agent can understand what it's working on after your action.

CRITICAL REMINDER: The update_llm_context call is NOT optional when:
- You are truncating any messages (MUST pair with update_llm_context)
- LLM Context is empty or missing (MUST call update_llm_context to initialize it)
- Task state has changed (MUST call update_llm_context to keep it current)

⚠️ IMMEDIATE ACTION REQUIRED:
If "Current LLM Context exists: false" above, you MUST call update_llm_context NOW with a minimal LLM Context containing:
- Current Task: [the latest user request from above]
- Files Involved: [any files mentioned in recent messages]
- Next Steps: [what the agent should work on next]

Failure to initialize LLM Context when it's empty will cause task continuity failures.

Messages marked [PROTECTED] are in the protected region and cannot be truncated.
Messages marked [NON_TRUNCATABLE:NO_ID] cannot be truncated because they have no tool call ID.
Only tool outputs with an explicit "id=" field are selectable for truncate_messages.

Available actions:
- **truncate_messages** - Remove old tool outputs to save space (specify IDs of outputs no longer needed).
- **update_llm_context** - Rewrite the LLM Context to reflect current state
- **compact** - Perform full context compaction by summarizing and removing old messages. Use this when many truncations have occurred and context is still under pressure, or a topic shift/task phase has been completed.
- **no_action** - Context is healthy, no action needed. DO NOT use no_action if LLM Context is empty.

Policy hint:
- force_truncate_recommended=%t
- If force_truncate_recommended is true, you SHOULD truncate_messages unless there's a strong reason not to.
- Consider using compact if total_compactions is 0 but total_truncations is high (>5) and tokens used is still above 40%%.`,
		truncatableCount,
		agentctx.RecentMessagesKeep,
		estimatedSavingsTokens,
		largeMessageCount,
		nonSelectableCount,
		truncatedCount,
		tokenPercent*100,
		agentCtx.AgentState.ToolCallsSinceLastTrigger,
		agentCtx.AgentState.TotalTurns,
		agentCtx.AgentState.TotalTruncations,
		agentCtx.AgentState.TotalCompactions,
		agentCtx.AgentState.LastCompactTurn,
		!llmContextEmpty,
		latestUserRequest,
		mgmtForceTruncateSavingsTokens,
		largeMessageCount,
		recommendedTruncate,
	)

	messages = append(messages, llm.LLMMessage{
		Role:    "user",
		Content: stateMsg,
	})

	return messages
}

// convertToolsToLLM converts context management tools to LLM format.
func (c *ContextManager) convertToolsToLLM(tools []context_mgmt.Tool) []llm.LLMTool {
	result := make([]llm.LLMTool, len(tools))
	for i, tool := range tools {
		result[i] = llm.LLMTool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  tool.Parameters(),
			},
		}
	}
	return result
}

// extractToolCalls reads tool calls from the LLM stream.
func (c *ContextManager) extractToolCalls(ctx context.Context, stream *llm.EventStream[llm.LLMEvent, llm.LLMMessage]) ([]llm.ToolCall, error) {
	var toolCalls []llm.ToolCall
	for event := range stream.Iterator(ctx) {
		if event.Done {
			break
		}
		switch e := event.Value.(type) {
		case llm.LLMDoneEvent:
			if e.Message != nil {
				toolCalls = append(toolCalls, e.Message.ToolCalls...)
			}
		case llm.LLMErrorEvent:
			return nil, e.Error
		}
	}
	return toolCalls, nil
}

// executeToolCalls runs each tool call and logs the result.
// Returns the count of truncated messages and whether LLM context was updated.
func (c *ContextManager) executeToolCalls(ctx context.Context, toolCalls []llm.ToolCall, tools []context_mgmt.Tool) (truncatedCount int, llmContextUpdated bool) {
	for _, tc := range toolCalls {
		startTime := time.Now()
		toolSpan := traceevent.StartSpan(ctx, "tool_execution", traceevent.CategoryTool,
			traceevent.Field{Key: "tool", Value: tc.Function.Name},
			traceevent.Field{Key: "tool_call_id", Value: tc.ID},
		)

		// Log tool_start event
		traceevent.Log(ctx, traceevent.CategoryTool, "tool_start",
			traceevent.Field{Key: "tool", Value: tc.Function.Name},
			traceevent.Field{Key: "tool_call_id", Value: tc.ID},
			traceevent.Field{Key: "args", Value: tc.Function.Arguments},
		)

		var target context_mgmt.Tool
		for _, tool := range tools {
			if tool.Name() == tc.Function.Name {
				target = tool
				break
			}
		}
		if target == nil {
			slog.Warn("[CtxMgmt] Tool not found", "tool", tc.Function.Name)
			toolSpan.AddField("error", true)
			toolSpan.AddField("error_message", fmt.Sprintf("tool %q not found", tc.Function.Name))
			toolSpan.End()
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: tc.Function.Name},
				traceevent.Field{Key: "tool_call_id", Value: tc.ID},
				traceevent.Field{Key: "duration_ms", Value: time.Since(startTime).Milliseconds()},
				traceevent.Field{Key: "error", Value: true},
				traceevent.Field{Key: "error_message", Value: fmt.Sprintf("tool %q not found", tc.Function.Name)},
			)
			continue
		}

		args := make(map[string]any)
		if tc.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				slog.Warn("[CtxMgmt] Failed to parse args", "tool", tc.Function.Name, "error", err)
				toolSpan.AddField("error", true)
				toolSpan.AddField("error_message", fmt.Sprintf("parse args: %v", err))
				toolSpan.End()
				traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
					traceevent.Field{Key: "tool", Value: tc.Function.Name},
					traceevent.Field{Key: "tool_call_id", Value: tc.ID},
					traceevent.Field{Key: "duration_ms", Value: time.Since(startTime).Milliseconds()},
					traceevent.Field{Key: "error", Value: true},
					traceevent.Field{Key: "error_message", Value: fmt.Sprintf("parse args: %v", err)},
				)
				continue
			}
		}

		content, err := target.Execute(ctx, args)
		if err != nil {
			slog.Error("[CtxMgmt] Tool execution failed", "tool", tc.Function.Name, "error", err)
			toolSpan.AddField("error", true)
			toolSpan.AddField("error_message", err.Error())
			toolSpan.End()
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: tc.Function.Name},
				traceevent.Field{Key: "tool_call_id", Value: tc.ID},
				traceevent.Field{Key: "duration_ms", Value: time.Since(startTime).Milliseconds()},
				traceevent.Field{Key: "error", Value: true},
				traceevent.Field{Key: "error_message", Value: err.Error()},
			)
			continue
		}

		resultText := ""
		if len(content) > 0 {
			if text, ok := content[0].(agentctx.TextContent); ok {
				resultText = text.Text
			}
		}
		slog.Info("[CtxMgmt] Tool executed", "tool", tc.Function.Name, "result", resultText)
		toolSpan.End()
		traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
			traceevent.Field{Key: "tool", Value: tc.Function.Name},
			traceevent.Field{Key: "tool_call_id", Value: tc.ID},
			traceevent.Field{Key: "duration_ms", Value: time.Since(startTime).Milliseconds()},
			traceevent.Field{Key: "result", Value: resultText},
		)

		// Track truncations and LLM context updates
		if tc.Function.Name == "truncate_messages" {
			// Extract count from result text
			var count int
			if _, err := fmt.Sscanf(resultText, "Truncated %d messages.", &count); err == nil {
				truncatedCount += count
			}
		} else if tc.Function.Name == "update_llm_context" {
			llmContextUpdated = true
		} else if tc.Function.Name == "compact" {
			// Compact tool updates AgentContext.RecentMessages, which affects LLM context
			llmContextUpdated = true
		}
	}
	return truncatedCount, llmContextUpdated
}

// extractLatestUserRequest finds the most recent user message text from the
// conversation to provide task grounding for the context management LLM.
// It searches from the end of the message list and returns up to 500 chars.
func extractLatestUserRequest(messages []agentctx.AgentMessage) string {
	const maxChars = 500
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role == "user" && msg.IsAgentVisible() {
			text := msg.ExtractText()
			if text == "" {
				continue
			}
			if len(text) > maxChars {
				return text[:maxChars] + "..."
			}
			return text
		}
	}
	return "(no user request found)"
}

// compactArgsStr returns a compact string representation of tool call arguments.
func compactArgsStr(args map[string]any) string {
	if args == nil || len(args) == 0 {
		return ""
	}
	b, _ := json.Marshal(args)
	s := string(b)
	if len(s) > 100 {
		return s[:100] + "..."
	}
	return s
}

package compact

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/tools/context_mgmt"
	
)

// Trigger thresholds for LLM mini compaction.
const (
	MiniTokenLow    = 0.20 // 20%: start periodic checks
	MiniTokenMedium = 0.40 // 40%: more aggressive checks
	MiniTokenHigh   = 0.60 // 60%: frequent checks

	MiniIntervalLow    = 30 // At 20%: every 30 tool calls
	MiniIntervalMedium = 15 // At 40%: every 15 tool calls
	MiniIntervalHigh   = 5  // At 60%: every 5 tool calls

	// Tool output preview limits for context management messages
	mgmtPreviewMax  = 1500
	mgmtPreviewHead = 800
	mgmtPreviewTail = 200
)

// LLMMiniCompactorConfig holds configuration.
type LLMMiniCompactorConfig struct {
	TokenLow    float64
	TokenMedium float64
	TokenHigh   float64

	IntervalLow    int
	IntervalMedium int
	IntervalHigh   int

	AutoCompact bool
}

// DefaultLLMMiniCompactorConfig returns defaults.
func DefaultLLMMiniCompactorConfig() *LLMMiniCompactorConfig {
	return &LLMMiniCompactorConfig{
		TokenLow:    MiniTokenLow,
		TokenMedium: MiniTokenMedium,
		TokenHigh:   MiniTokenHigh,

		IntervalLow:    MiniIntervalLow,
		IntervalMedium: MiniIntervalMedium,
		IntervalHigh:   MiniIntervalHigh,

		AutoCompact: true,
	}
}

// LLMMiniCompactor performs lightweight LLM-driven context management.
// It is triggered periodically by the agent loop and makes an independent LLM
// call with context-management-specific tools (truncate_messages, update_llm_context, no_action).
// The main LLM is never involved in context management decisions.
type LLMMiniCompactor struct {
	config        *LLMMiniCompactorConfig
	model         llm.Model
	apiKey        string
	contextWindow int
	systemPrompt  string
}

// NewLLMMiniCompactor creates a new LLMMiniCompactor.
func NewLLMMiniCompactor(
	config *LLMMiniCompactorConfig,
	model llm.Model,
	apiKey string,
	contextWindow int,
	systemPrompt string,
) *LLMMiniCompactor {
	if config == nil {
		config = DefaultLLMMiniCompactorConfig()
	}
	return &LLMMiniCompactor{
		config:        config,
		model:         model,
		apiKey:        apiKey,
		contextWindow: contextWindow,
		systemPrompt:  systemPrompt,
	}
}

// ShouldCompact checks if the compactor should run.
// It uses token percentage and tool-call interval to decide.
func (c *LLMMiniCompactor) ShouldCompact(ctx *agentctx.AgentContext) bool {
	if !c.config.AutoCompact {
		return false
	}

	tokenPercent := c.estimateTokenPercent(ctx)
	if tokenPercent < c.config.TokenLow {
		return false
	}

	toolCallsSince := ctx.AgentState.ToolCallsSinceLastTrigger
	var interval int
	switch {
	case tokenPercent >= c.config.TokenHigh:
		interval = c.config.IntervalHigh
	case tokenPercent >= c.config.TokenMedium:
		interval = c.config.IntervalMedium
	default:
		interval = c.config.IntervalLow
	}

	return toolCallsSince >= interval
}

// Compact runs the LLM-driven context management cycle.
func (c *LLMMiniCompactor) Compact(agentCtx *agentctx.AgentContext) (*agentctx.CompactionResult, error) {
	start := time.Now()
	tokensBefore := agentCtx.EstimateTokens()

	slog.Info("[LLMMini] Starting compact",
		"messages", len(agentCtx.RecentMessages),
		"token_pct", fmt.Sprintf("%.1f%%", c.estimateTokenPercent(agentCtx)*100),
	)

	// 1. Build context management messages (full conversation with annotations)
	messages := c.buildContextMgmtMessages(agentCtx)

	// 2. Get context management tools
	tools := context_mgmt.GetMiniCompactTools(agentCtx)

	// 3. Call LLM
	llmMessages := append([]llm.LLMMessage{{
		Role:    "system",
		Content: c.systemPrompt,
	}}, messages...)

	stream := llm.StreamLLM(
		context.Background(),
		c.model,
		llm.LLMContext{
			Messages: llmMessages,
			Tools:    c.convertToolsToLLM(tools),
		},
		c.apiKey,
		2*time.Minute,
	)

	// 4. Extract tool calls from stream
	toolCalls, err := c.extractToolCalls(context.Background(), stream)
	if err != nil {
		slog.Error("[LLMMini] LLM call failed", "error", err)
		return nil, fmt.Errorf("context management LLM call failed: %w", err)
	}

	// 5. Execute tool calls
	c.executeToolCalls(context.Background(), toolCalls, tools)

	// 6. Reset trigger counters
	agentCtx.AgentState.LastTriggerTurn = agentCtx.AgentState.TotalTurns
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 0
	agentCtx.AgentState.UpdatedAt = time.Now()

	tokensAfter := agentCtx.EstimateTokens()
	duration := time.Since(start)

	slog.Info("[LLMMini] Compact complete",
		"tokens_before", tokensBefore,
		"tokens_after", tokensAfter,
		"saved", tokensBefore-tokensAfter,
		"tool_calls", len(toolCalls),
		"duration", duration,
	)

	return &agentctx.CompactionResult{
		Summary:      fmt.Sprintf("LLM mini compact: %d tool calls executed", len(toolCalls)),
		TokensBefore: tokensBefore,
		TokensAfter:  tokensAfter,
	}, nil
}

// CalculateDynamicThreshold returns the token threshold for compaction.
func (c *LLMMiniCompactor) CalculateDynamicThreshold() int {
	if c.contextWindow <= 0 {
		return 0
	}
	return int(float64(c.contextWindow) * c.config.TokenLow)
}

// EstimateContextTokens estimates the token count of context.
func (c *LLMMiniCompactor) EstimateContextTokens(ctx *agentctx.AgentContext) int {
	return ctx.EstimateTokens()
}

// --- Internal helpers ---

func (c *LLMMiniCompactor) estimateTokenPercent(ctx *agentctx.AgentContext) float64 {
	if c.contextWindow <= 0 {
		return 0
	}
	return float64(ctx.EstimateTokens()) / float64(c.contextWindow)
}

// buildContextMgmtMessages builds the message sequence for context management.
// Sends the FULL conversation with annotations so the LLM can judge
// whether each tool output is still useful.
func (c *LLMMiniCompactor) buildContextMgmtMessages(agentCtx *agentctx.AgentContext) []llm.LLMMessage {
	protectedStart := len(agentCtx.RecentMessages) - agentctx.RecentMessagesKeep
	if protectedStart < 0 {
		protectedStart = 0
	}

	// Count truncatable and already-truncated outputs
	truncatableCount := 0
	truncatedCount := 0
	for i := range protectedStart {
		if i < len(agentCtx.RecentMessages) {
			msg := agentCtx.RecentMessages[i]
			if msg.Role == "toolResult" {
				if msg.Truncated {
					truncatedCount++
				} else {
					truncatableCount++
				}
			}
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

	// State message as the final user message
	tokenPercent := c.estimateTokenPercent(agentCtx)
	stateMsg := fmt.Sprintf(`<current_state>
Truncatable tool outputs: %d (protected region: last %d messages)
Already truncated outputs: %d
Tokens used: %.1f%%
Tool calls since last management: %d
Total turns: %d
</current_state>

Review the conversation above and decide the best action.

**Key decision signal**: If "Already truncated outputs" is high (≥10), truncate has diminishing returns — consider that context may need a full compact instead.

Messages marked [PROTECTED] are in the protected region and cannot be truncated. Only tool outputs with an "id=" field are truncatable.

Available actions:
- **truncate_messages** - Remove old tool outputs to save space (specify IDs of outputs no longer needed). Best when truncated_count is low.
- **update_llm_context** - Rewrite the LLM Context to reflect current state
- **no_action** - Context is healthy, no action needed`,
		truncatableCount,
		agentctx.RecentMessagesKeep,
		truncatedCount,
		tokenPercent*100,
		agentCtx.AgentState.ToolCallsSinceLastTrigger,
		agentCtx.AgentState.TotalTurns,
	)

	messages = append(messages, llm.LLMMessage{
		Role:    "user",
		Content: stateMsg,
	})

	return messages
}

// convertToolsToLLM converts context management tools to LLM format.
func (c *LLMMiniCompactor) convertToolsToLLM(tools []context_mgmt.Tool) []llm.LLMTool {
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
func (c *LLMMiniCompactor) extractToolCalls(ctx context.Context, stream *llm.EventStream[llm.LLMEvent, llm.LLMMessage]) ([]llm.ToolCall, error) {
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
func (c *LLMMiniCompactor) executeToolCalls(ctx context.Context, toolCalls []llm.ToolCall, tools []context_mgmt.Tool) {
	for _, tc := range toolCalls {
		var target context_mgmt.Tool
		for _, tool := range tools {
			if tool.Name() == tc.Function.Name {
				target = tool
				break
			}
		}
		if target == nil {
			slog.Warn("[LLMMini] Tool not found", "tool", tc.Function.Name)
			continue
		}

		args := make(map[string]any)
		if tc.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				slog.Warn("[LLMMini] Failed to parse args", "tool", tc.Function.Name, "error", err)
				continue
			}
		}

		content, err := target.Execute(ctx, args)
		if err != nil {
			slog.Error("[LLMMini] Tool execution failed", "tool", tc.Function.Name, "error", err)
			continue
		}

		resultText := ""
		if len(content) > 0 {
			if text, ok := content[0].(agentctx.TextContent); ok {
				resultText = text.Text
			}
		}
		slog.Info("[LLMMini] Tool executed", "tool", tc.Function.Name, "result", resultText)
	}
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

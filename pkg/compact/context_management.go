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

	// Heuristic age thresholds for marking tool outputs as likely stale.
	// bash/read/grep/find are typically one-shot investigative results.
	mgmtStaleAgeInvestigative = 20
	// edit/write confirm modifications usually already reflected in later work.
	mgmtStaleAgeModification = 30
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

	// StaleAnnotation controls whether tool outputs are annotated with
	// age and likely_stale markers in context management messages.
	// When false (default), the behavior matches the main branch (no annotation).
	StaleAnnotation bool

	// StaleAgeInvestigative is the age threshold for investigative tools
	// (bash/read/grep/find). Default 20 if not set.
	StaleAgeInvestigative int

	// StaleAgeModification is the age threshold for modification tools
	// (edit/write). Default 30 if not set.
	StaleAgeModification int

	// ContextMgmtPrompt overrides the built-in context management system prompt.
	// If empty, the default from pkg/prompt is used.
	ContextMgmtPrompt string

	// SkipCondition, when set and returns true, causes ShouldCompact to return false.
	// This is used to skip proactive LLM-driven context management in cache-first mode,
	// where preserving the stable message prefix is more important.
	// Full compaction (75% threshold) remains active regardless.
	SkipCondition func() bool
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

// staleAgeInvestigative returns the configured investigative stale threshold,
// defaulting to mgmtStaleAgeInvestigative if not set.
func (c *ContextManagerConfig) staleAgeInvestigative() int {
	if c.StaleAgeInvestigative <= 0 {
		return mgmtStaleAgeInvestigative
	}
	return c.StaleAgeInvestigative
}

// staleAgeModification returns the configured modification stale threshold,
// defaulting to mgmtStaleAgeModification if not set.
func (c *ContextManagerConfig) staleAgeModification() int {
	if c.StaleAgeModification <= 0 {
		return mgmtStaleAgeModification
	}
	return c.StaleAgeModification
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
	// Use custom prompt if provided via config, otherwise use the passed-in default.
	effectivePrompt := systemPrompt
	if config.ContextMgmtPrompt != "" {
		effectivePrompt = config.ContextMgmtPrompt
	}
	return &ContextManager{
		config:        config,
		model:         model,
		apiKey:        apiKey,
		contextWindow: contextWindow,
		systemPrompt:  effectivePrompt,
		compactor:     compactor,
	}
}

// SetCompactor sets the full compactor for compact tool support.
func (c *ContextManager) SetCompactor(compactor *Compactor) {
	c.compactor = compactor
}

// SetSkipCondition sets a function that, when returning true, causes ShouldCompact
// to skip proactive LLM-driven context management. This allows callers (e.g. rpcApp)
// to inject a model-aware skip condition without creating import cycles.
func (c *ContextManager) SetSkipCondition(fn func() bool) {
	c.config.SkipCondition = fn
}

// ShouldCompact checks if the compactor should run.
// It uses token percentage and tool-call interval to decide.
func (c *ContextManager) ShouldCompact(ctx context.Context, agentCtx *agentctx.AgentContext) bool {
	if c.config.SkipCondition != nil && c.config.SkipCondition() {
		traceevent.Log(ctx, traceevent.CategoryEvent, "context_mgmt_check",
			traceevent.Field{Key: "decision", Value: false},
			traceevent.Field{Key: "reason", Value: "skipped_cache_first_mode"},
		)
		return false
	}

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
func (c *ContextManager) Compact(ctx context.Context, agentCtx *agentctx.AgentContext) (*agentctx.CompactionResult, error) {
	return c.CompactWithCtx(ctx, agentCtx)
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

	// 3. Call LLM with retry for transient errors
	llmMessages := append([]llm.LLMMessage{{
		Role:    "system",
		Content: c.systemPrompt,
	}}, messages...)

	const maxRetries = 3
	var toolCalls []llm.ToolCall
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Respect context cancellation between retries.
		if err := parent.Err(); err != nil {
			slog.Error("[CtxMgmt] Context cancelled before LLM attempt", "attempt", attempt, "error", err)
			return nil, fmt.Errorf("context management LLM call failed: %w", err)
		}

		llmSpan := traceevent.StartSpan(parent, "llm_call", traceevent.CategoryLLM,
			traceevent.Field{Key: "model", Value: c.model.ID},
			traceevent.Field{Key: "provider", Value: c.model.Provider},
			traceevent.Field{Key: "api", Value: c.model.API},
			traceevent.Field{Key: "attempt", Value: attempt},
			traceevent.Field{Key: "caller", Value: "context_management"},
		)
		llmCallStart := time.Now()

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
		var err error
		var usage llm.Usage
		var firstTokenLatency time.Duration
		toolCalls, usage, firstTokenLatency, err = c.extractToolCalls(parent, stream)

		// Add token usage metrics to span
		if usage.InputTokens > 0 || usage.OutputTokens > 0 {
			llmSpan.AddField("input_tokens", usage.InputTokens)
			llmSpan.AddField("output_tokens", usage.OutputTokens)
			llmSpan.AddField("total_tokens", usage.TotalTokens)
			if usage.PromptTokensDetails != nil {
				llmSpan.AddField("cache_read", usage.PromptTokensDetails.CachedTokens)
			}
			duration := time.Since(llmCallStart)
			if duration.Seconds() > 0 {
				llmSpan.AddField("input_tokens_per_sec", float64(usage.InputTokens)/duration.Seconds())
				llmSpan.AddField("output_tokens_per_sec", float64(usage.OutputTokens)/duration.Seconds())
			}
		}
		if firstTokenLatency > 0 {
			llmSpan.AddField("first_token_ms", firstTokenLatency.Milliseconds())
		}

		if err != nil {
			llmSpan.AddField("error", err.Error())
			llmSpan.AddField("retryable", llm.IsRetryableError(err))
			traceevent.Log(parent, traceevent.CategoryLLM, "llm_call_error",
				traceevent.Field{Key: "caller", Value: "context_management"},
				traceevent.Field{Key: "attempt", Value: attempt},
				traceevent.Field{Key: "error", Value: err.Error()},
				traceevent.Field{Key: "retryable", Value: llm.IsRetryableError(err)},
			)
		}
		llmSpan.End()

		if err == nil {
			lastErr = nil
			break
		}

		lastErr = err
		if !llm.IsRetryableError(err) {
			slog.Error("[CtxMgmt] LLM call failed (non-retryable)", "attempt", attempt, "error", err)
			return nil, fmt.Errorf("context management LLM call failed: %w", err)
		}

		if attempt < maxRetries {
			backoff := time.Duration(attempt) * 2 * time.Second
			slog.Warn("[CtxMgmt] LLM call failed (retryable), retrying",
				"attempt", attempt,
				"max_retries", maxRetries,
				"backoff", backoff,
				"error", err,
			)
			select {
			case <-parent.Done():
				return nil, fmt.Errorf("context management LLM call failed: %w", parent.Err())
			case <-time.After(backoff):
			}
		}
	}

	if lastErr != nil {
		slog.Error("[CtxMgmt] LLM call failed after all retries", "attempts", maxRetries, "error", lastErr)
		return nil, fmt.Errorf("context management LLM call failed: %w", lastErr)
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

	// Return nil when no actual work was performed so the compaction loop can
	// fall through to the next compactor (e.g. full session compaction).
	if truncatedCount == 0 && !llmContextUpdated && len(toolCalls) == 0 {
		slog.Info("[CtxMgmt] No action taken, returning nil to allow fallback compactor")
		return nil, nil
	}

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
	LikelyStale  bool // heuristic: tool output is probably no longer needed
	Age          int  // number of messages between this output and the protected region
}

func collectTruncationCandidates(agentCtx *agentctx.AgentContext, protectedStart int, staleAnnotation bool, staleAgeInvestigative int, staleAgeModification int) ([]truncationCandidate, int, int) {
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

		age := protectedStart - i

		candidates = append(candidates, truncationCandidate{
			ID:           id,
			ToolName:     msg.ToolName,
			Chars:        len(text),
			SavingsToken: savedChars / 4,
			Age:          age,
			LikelyStale:  staleAnnotation && isLikelyStale(msg.ToolName, age, staleAgeInvestigative, staleAgeModification),
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

// isLikelyStale returns true when a tool output is heuristically judged as
// probably no longer needed. This is a conservative signal — the LLM can
// override it when the output contains content relevant to the current task.
//
// Rules:
//   - bash/read/grep/find outputs older than staleAgeInvestigative: typically one-shot
//     investigative results whose findings are already captured.
//   - edit/write outputs older than staleAgeModification: confirm modifications, but
//     the change is usually already reflected in later work.
//   - Other tool types: not marked stale (insufficient signal).
func isLikelyStale(toolName string, age int, staleAgeInvestigative int, staleAgeModification int) bool {
	switch toolName {
	case "bash", "read", "grep", "find":
		return age > staleAgeInvestigative
	case "edit", "write":
		return age > staleAgeModification
	default:
		return false
	}
}

// buildContextMgmtMessages builds the message sequence for context management.
// Sends the FULL conversation with annotations so the LLM can judge
// whether each tool output is still useful.
func (c *ContextManager) buildContextMgmtMessages(agentCtx *agentctx.AgentContext) []llm.LLMMessage {
	protectedStart := len(agentCtx.RecentMessages) - agentctx.RecentMessagesKeep
	if protectedStart < 0 {
		protectedStart = 0
	}

	candidates, truncatedCount, nonSelectableCount := collectTruncationCandidates(
		agentCtx, protectedStart,
		c.config.StaleAnnotation,
		c.config.staleAgeInvestigative(),
		c.config.staleAgeModification(),
	)
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

	// Build candidate lookup by ID for reuse in annotations
	candidateByID := make(map[string]truncationCandidate, len(candidates))
	for _, cand := range candidates {
		candidateByID[cand.ID] = cand
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
				if c.config.StaleAnnotation {
					// Annotate with age and likely_stale when enabled
					age := protectedStart - msgIdx
					staleTag := ""
					if cand, ok := candidateByID[msg.ToolCallID]; ok && cand.LikelyStale {
						age = cand.Age
						staleTag = " likely_stale=true"
					}
					conv.WriteString(fmt.Sprintf("[tool:%s chars=%d age=%d%s] id=%s\n%s\n\n",
						msg.ToolName, len(msg.ExtractText()), age, staleTag, msg.ToolCallID, content))
				} else {
					// Default behavior: no age/stale annotation
					conv.WriteString(fmt.Sprintf("[tool:%s chars=%d] id=%s\n%s\n\n",
						msg.ToolName, len(msg.ExtractText()), msg.ToolCallID, content))
				}
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
Total truncations so far: %d
Total compactions so far: %d
Current LLM Context exists: %t
force_truncate_recommended=%t
</current_state>

<latest_user_request>
%s
</latest_user_request>

Review the conversation and decide: truncate old outputs, update LLM Context, compact, or no_action.
Outputs marked "likely_stale=true" are strong truncation candidates.
If force_truncate_recommended is true, you SHOULD truncate unless there's a strong reason not to.
If LLM Context does not exist, you MUST call update_llm_context.`,
		truncatableCount,
		agentctx.RecentMessagesKeep,
		estimatedSavingsTokens,
		largeMessageCount,
		nonSelectableCount,
		truncatedCount,
		tokenPercent*100,
		agentCtx.AgentState.ToolCallsSinceLastTrigger,
		agentCtx.AgentState.TotalTruncations,
		agentCtx.AgentState.TotalCompactions,
		!llmContextEmpty,
		recommendedTruncate,
		latestUserRequest,
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
func (c *ContextManager) extractToolCalls(ctx context.Context, stream *llm.EventStream[llm.LLMEvent, llm.LLMMessage]) ([]llm.ToolCall, llm.Usage, time.Duration, error) {
	var toolCalls []llm.ToolCall
	var usage llm.Usage
	var firstTokenLatency time.Duration
	llmStart := time.Now()
	firstToken := true

	for event := range stream.Iterator(ctx) {
		if event.Done {
			break
		}
		switch e := event.Value.(type) {
		case llm.LLMDoneEvent:
			if e.Message != nil {
				toolCalls = append(toolCalls, e.Message.ToolCalls...)
			}
			usage = e.Usage
		case llm.LLMTextDeltaEvent:
			if firstToken {
				firstTokenLatency = time.Since(llmStart)
				firstToken = false
			}
		case llm.LLMThinkingDeltaEvent:
			if firstToken {
				firstTokenLatency = time.Since(llmStart)
				firstToken = false
			}
		case llm.LLMErrorEvent:
			return nil, usage, firstTokenLatency, e.Error
		}
	}
	return toolCalls, usage, firstTokenLatency, nil
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

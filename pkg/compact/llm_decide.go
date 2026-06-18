package compact

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// LargeContextThreshold is the minimum context window size for using
// LLMDecideCompactor instead of ContextManager. Models with smaller windows
// keep the old truncate/update cycle.
const LargeContextThreshold = 500_000

// LLMDecideConfig configures the LLM-decides compactor.
type LLMDecideConfig struct {
	// SoftThreshold: tokens before periodic checks begin.
	SoftThreshold int
	// HardLimit: tokens where compaction is forced without asking.
	HardLimit int
	// TierMedium: token level to switch from low to medium interval.
	TierMedium int
	// TierHigh: token level to switch from medium to high interval.
	TierHigh int
	// IntervalLow/Medium/High: tool calls between checks per tier.
	IntervalLow    int
	IntervalMedium int
	IntervalHigh   int
}

// DefaultLLMDecideConfig returns thresholds scaled to the context window.
//
// For 1M context: soft=80K, tiers at 100K/120K, hard=300K.
// Smaller windows scale proportionally, capped at the 1M values.
func DefaultLLMDecideConfig(contextWindow int) LLMDecideConfig {
	soft := min(contextWindow*8/100, 80_000)
	hard := min(contextWindow*30/100, 300_000)
	return LLMDecideConfig{
		SoftThreshold:  soft,
		HardLimit:      hard,
		TierMedium:     soft + 20_000,
		TierHigh:       soft + 40_000,
		IntervalLow:    15,
		IntervalMedium: 10,
		IntervalHigh:   7,
	}
}

// LLMDecideCompactor is a compactor for large context window models.
//
// Instead of the old ContextManager (separate system prompt + truncate/update
// tools that break cache), it reuses the main conversation prefix (cache hit)
// and periodically asks the LLM whether to compact. A hard limit forces
// compaction without asking.
//
// Actual compaction (summary generation) is delegated to *Compactor.
type LLMDecideCompactor struct {
	config        LLMDecideConfig
	model         llm.Model
	apiKey        string
	contextWindow int
	compactor     *Compactor
	askPrompt     string // template with single %s for budget percentage
}

// NewLLMDecideCompactor creates an LLMDecideCompactor.
func NewLLMDecideCompactor(config LLMDecideConfig, model llm.Model, apiKey string, contextWindow int, compactor *Compactor) *LLMDecideCompactor {
	return &LLMDecideCompactor{
		config:        config,
		model:         model,
		apiKey:        apiKey,
		contextWindow: contextWindow,
		compactor:     compactor,
		askPrompt:     prompt.LLMDecideCheckPrompt(),
	}
}

// ShouldCompact returns true when a compaction check should run.
//
// - Below SoftThreshold: never (no intervention).
// - At or above HardLimit: always (force compact).
// - Between: staircase intervals based on tool call count and token tier.
func (c *LLMDecideCompactor) ShouldCompact(_ context.Context, agentCtx *agentctx.AgentContext) bool {
	tokens := agentCtx.EstimateTokens()

	if tokens >= c.config.HardLimit {
		return true
	}
	if tokens < c.config.SoftThreshold {
		return false
	}

	interval := c.getInterval(tokens)
	return agentCtx.AgentState.ToolCallsSinceLastTrigger >= interval
}

func (c *LLMDecideCompactor) getInterval(tokens int) int {
	switch {
	case tokens >= c.config.TierHigh:
		return c.config.IntervalHigh
	case tokens >= c.config.TierMedium:
		return c.config.IntervalMedium
	default:
		return c.config.IntervalLow
	}
}

// Compact either forces compaction (hard limit) or asks the LLM first.
//
// The ask call reuses the main conversation prefix (system prompt + tools +
// messages) for maximum cache hit. Only the trailing question message and the
// response are cache misses.
func (c *LLMDecideCompactor) Compact(ctx context.Context, agentCtx *agentctx.AgentContext) (*agentctx.CompactionResult, error) {
	tokens := agentCtx.EstimateTokens()

	// Hard limit: force compact without asking.
	if tokens >= c.config.HardLimit {
		slog.Info("[LLMDecide] Hard limit reached, forcing compact",
			"tokens", tokens, "hard_limit", c.config.HardLimit)
		return c.doCompact(ctx, agentCtx)
	}

	// Ask the LLM whether to compact.
	shouldDo, err := c.askLLM(ctx, agentCtx, tokens)
	if err != nil {
		slog.Warn("[LLMDecide] Ask failed, compacting as fallback", "error", err)
		return c.doCompact(ctx, agentCtx)
	}

	// Reset the tool-call counter after each check (even "no") so we don't
	// ask again every single turn.
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 0

	if !shouldDo {
		slog.Info("[LLMDecide] LLM decided not to compact",
			"tokens", tokens,
			"budget_pct", fmt.Sprintf("%.0f%%", float64(tokens)/float64(c.config.HardLimit)*100))
		return nil, nil // no-op; let the next compactor try
	}

	slog.Info("[LLMDecide] LLM decided to compact",
		"tokens", tokens,
		"budget_pct", fmt.Sprintf("%.0f%%", float64(tokens)/float64(c.config.HardLimit)*100))
	return c.doCompact(ctx, agentCtx)
}

// doCompact delegates to the underlying Compactor and resets the counter.
func (c *LLMDecideCompactor) doCompact(ctx context.Context, agentCtx *agentctx.AgentContext) (*agentctx.CompactionResult, error) {
	result, err := c.compactor.Compact(ctx, agentCtx)
	if err == nil && result != nil {
		agentCtx.AgentState.ToolCallsSinceLastTrigger = 0
	}
	return result, err
}

// askLLM sends a lightweight yes/no question to the LLM, reusing the main
// conversation prefix for cache efficiency. Returns true if the LLM says yes.
func (c *LLMDecideCompactor) askLLM(ctx context.Context, agentCtx *agentctx.AgentContext, tokens int) (bool, error) {
	span := traceevent.StartSpan(ctx, "llm_decide.ask", traceevent.CategoryLLM)
	defer span.End()

	budgetPct := fmt.Sprintf("%d%%", tokens*100/c.config.HardLimit)
	askContent := fmt.Sprintf(c.askPrompt, budgetPct)

	span.AddField("tokens", tokens)
	span.AddField("budget_pct", budgetPct)

	// Build messages identical to a normal agent turn so the prefix is a cache hit.
	llmMessages := agentctx.ConvertMessagesToLLM(agentCtx.RecentMessages)

	// Prepend contextPrefix (skills + AGENTS.md) as a user message, matching the agent loop.
	if strings.TrimSpace(agentCtx.AgentContextPrefix) != "" {
		llmMessages = append([]llm.LLMMessage{{
			Role:    "user",
			Content: agentCtx.AgentContextPrefix,
		}}, llmMessages...)
	}

	// Append the compact-check question as a trailing user message.
	llmMessages = append(llmMessages, llm.LLMMessage{
		Role:    "user",
		Content: askContent,
	})

	llmCtx := llm.LLMContext{
		SystemPrompt: agentCtx.SystemPrompt,
		Messages:     llmMessages,
		Tools:        agentctx.ConvertToolsToLLM(agentCtx.Tools),
	}

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	stream := llm.StreamLLM(callCtx, c.model, llmCtx, c.apiKey, 60*time.Second)

	var response strings.Builder
	for event := range stream.Iterator(callCtx) {
		if event.Done {
			break
		}
		switch e := event.Value.(type) {
		case llm.LLMTextDeltaEvent:
			response.WriteString(e.Delta)
		case llm.LLMErrorEvent:
			return false, e.Error
		}
	}

	answer := strings.ToLower(strings.TrimSpace(response.String()))
	yes := strings.Contains(answer, "yes")

	span.AddField("response", response.String())
	span.AddField("decision", yes)

	return yes, nil
}

// CalculateDynamicThreshold returns the hard limit for telemetry/compat.
func (c *LLMDecideCompactor) CalculateDynamicThreshold() int {
	return c.config.HardLimit
}

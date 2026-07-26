package compact

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// Config contains configuration for context compression.
type Config struct {
	MaxMessages      int // Maximum messages before compression
	MaxTokens        int // Approximate token limit before compression
	KeepRecent       int // Number of recent messages to keep
	KeepRecentTokens int // Token budget to keep from the recent messages
	ReserveTokens    int // Tokens to reserve when using context window
	ToolCallCutoff   int // Summarize oldest tool outputs when visible tool calls exceed this
	// ToolSummaryAutomation controls when background tool-output summary runs:
	// - off: disable automatic tool-output summary
	// - fallback: only run when compactor pressure fallback is triggered
	// - always: run whenever ToolCallCutoff is exceeded
	ToolSummaryAutomation string
	// GracePeriod protects the N most recent tool results from being archived during
	// tool call pairing check. This allows tool calls that span compaction boundaries
	// to complete without their results being hidden. Default is 1 (the most recent).
	GracePeriod int
	AutoCompact bool // Whether to automatically compact

	// LLMDecide enables LLM-decides compaction mode for large context windows.
	// When set, ShouldCompact uses soft/hard thresholds + tool-call intervals,
	// and asks the LLM whether to compact when an interval is reached.
	// A hard limit forces compaction without asking.
	LLMDecide *LLMDecideConfig
}

// LLMDecideConfig configures the LLM-decides compaction strategy.
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

// DefaultLLMDecideConfig returns tuned thresholds for the given context window.
//
// These values are empirically tuned per context-window tier, not derived from
// a single formula. Update them only when you have usage data to justify it.
//
//	1M context:  soft=80K(8%),  tiers=100K/120K,  hard=200K(20%)
//	200K context: soft=40K(20%), tiers=70K/100K,  hard=150K(75%)
func DefaultLLMDecideConfig(contextWindow int) LLMDecideConfig {
	switch {
	case contextWindow >= 800_000: // 1M-class models
		return LLMDecideConfig{
			SoftThreshold:  80_000,
			HardLimit:      200_000,
			TierMedium:     100_000,
			TierHigh:       120_000,
			IntervalLow:    15,
			IntervalMedium: 10,
			IntervalHigh:   7,
		}
	case contextWindow > 0: // Known context window (e.g. 200K)
		pct := func(p int) int { return contextWindow * p / 100 }
		return LLMDecideConfig{
			SoftThreshold:  pct(25),
			HardLimit:      pct(75),
			TierMedium:     pct(35),
			TierHigh:       pct(50),
			IntervalLow:    15,
			IntervalMedium: 10,
			IntervalHigh:   7,
		}
	default: // Unknown context window (0 or negative) — use 200K defaults
		return LLMDecideConfig{
			SoftThreshold:  50_000,
			HardLimit:      150_000,
			TierMedium:     70_000,
			TierHigh:       100_000,
			IntervalLow:    15,
			IntervalMedium: 10,
			IntervalHigh:   7,
		}
	}
}

// DefaultConfig returns default compression configuration.
func DefaultConfig() *Config {
	return &Config{
		MaxMessages:           50,    // Compact after 50 messages
		MaxTokens:             8000,  // Compact after ~8000 tokens (fallback)
		KeepRecent:            5,     // Keep last 5 messages uncompressed
		KeepRecentTokens:      20000, // Keep ~20k tokens from the recent context
		ReserveTokens:         16384, // Reserve tokens for responses when using context window
		ToolCallCutoff:        10,    // Summarize tool outputs after 10 visible tool results
		ToolSummaryAutomation: "off", // Automatic tool-output summary (off, fallback, always)
		GracePeriod:           1,     // Protect 1 most recent tool result by default
		AutoCompact:           true,  // Automatic context compression at 75% threshold
	}
}

// Compactor handles context compression.
type Compactor struct {
	config        *Config
	model         llm.Model
	apiKey        string
	systemPrompt  string
	contextWindow int
	askPrompt     string // LLM-decide ask template (loaded lazily)
	// agentContextPrefix is the skills + AGENTS.md prefix, stored at
	// construction time so it survives agentCtx checkpoint/restore cycles
	// (AgentContext.AgentContextPrefix has json:"-" and is lost on restore).
	agentContextPrefix string
	// thinkingLevel mirrors the agent loop's thinking level so that
	// askLLM/GenerateSummary requests include the same thinking/reasoning
	// parameters, keeping them in the same prefix-cache partition.
	thinkingLevel string
	// sessionDir is the session directory used for archiving old messages
	// that are removed during compaction. When empty, archiving is skipped.
	sessionDir string
	// askFunc allows tests to inject a fake LLM decision without a real API
	// call. nil means use the real askLLM method.
	askFunc func(ctx context.Context, agentCtx *agentctx.AgentContext, tokens int) (bool, error)

	// llmDecideLastAskCount tracks the tool-call counter value at the last
	// LLM-decide ask, preventing re-asking every turn after a "no".
	llmDecideLastAskCount int
}

// NewCompactor creates a new Compactor.
func NewCompactor(config *Config, model llm.Model, apiKey, systemPrompt string, contextWindow int, sessionDir string) *Compactor {
	if config == nil {
		config = DefaultConfig()
	}
	return &Compactor{
		config:        config,
		model:         model,
		apiKey:        apiKey,
		systemPrompt:  systemPrompt,
		contextWindow: contextWindow,
		sessionDir:    sessionDir,
	}
}

// GetConfig returns the compactor configuration.
func (c *Compactor) GetConfig() *Config {
	return c.config
}

// CalculateDynamicThreshold calculates the compaction threshold based on context window.
// For models with large context windows (e.g., 128k), this allows much more context
// before triggering compaction, rather than using a fixed 8000 token limit.
// CalculateDynamicThreshold returns the dynamic compaction threshold based on context window.
// Exported for use by context_management tool to provide feedback when compact is rejected.
func (c *Compactor) CalculateDynamicThreshold() int {
	// If context window is known, calculate dynamic threshold
	if c.contextWindow > 0 {
		// Reserve tokens for:
		// - System prompt (~5k estimated)
		// - Tool definitions (~3k estimated)
		// - Output generation (16k reserve)
		// - Safety margin (20% of available)

		systemTokens := estimateStringTokens(c.systemPrompt)
		toolTokens := 3000 // Average tool definitions
		reserveTokens := c.ReserveTokens()

		overhead := systemTokens + toolTokens + reserveTokens
		available := c.contextWindow - overhead

		if available <= 0 {
			// Fallback to configured max tokens if window is too small
			return c.config.MaxTokens
		}

		// Use 75% of available as compaction threshold
		// This leaves 25% buffer before hitting context limit
		threshold := int(float64(available) * 0.75)

		// Ensure minimum threshold
		minThreshold := 4000
		if threshold < minThreshold {
			threshold = minThreshold
		}

		return threshold
	}

	// Fallback to configured max tokens
	return c.config.MaxTokens
}

// calculateKeepRecentBudget calculates the token budget for keeping recent messages.
// This scales with the context window rather than using a fixed value.
func (c *Compactor) calculateKeepRecentBudget() int {
	// If a fixed budget is configured, respect it (but cap it)
	if c.config.KeepRecentTokens > 0 {
		budget := c.config.KeepRecentTokens

		// Don't let keep-recent exceed 30% of available context
		if threshold := c.CalculateDynamicThreshold(); threshold > 0 {
			maxKeep := int(float64(threshold) * 0.3)
			if budget > maxKeep && maxKeep > 0 {
				budget = maxKeep
			}
		}

		return budget
	}

	// Calculate based on threshold
	threshold := c.CalculateDynamicThreshold()
	if threshold > 0 {
		// Keep 25% of threshold as recent context
		return int(float64(threshold) * 0.25)
	}

	// Fallback to default
	return 20000
}

// estimateStringTokens provides a rough token estimation for a string.
func estimateStringTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	// Rough approximation: 1 token per 4 characters
	return int(float64(len(s)) / 4.0)
}

// ContextWindow returns the configured model context window.
func (c *Compactor) ContextWindow() int {
	return c.contextWindow
}

// SetContextWindow updates the model context window used for compaction.
func (c *Compactor) SetContextWindow(window int) {
	c.contextWindow = window
}

// SetAgentContextPrefix updates the skills + AGENTS.md prefix used for
// cache-friendly LLM requests (askLLM, GenerateSummary).
func (c *Compactor) SetAgentContextPrefix(prefix string) {
	c.agentContextPrefix = prefix
}

// SetThinkingLevel sets the thinking level used in askLLM/GenerateSummary
// requests so they match the agent loop's thinking/reasoning parameters.
func (c *Compactor) SetThinkingLevel(level string) {
	c.thinkingLevel = level
}

// ReserveTokens returns the effective reserve tokens setting.
func (c *Compactor) ReserveTokens() int {
	if c.config == nil || c.config.ReserveTokens <= 0 {
		return DefaultConfig().ReserveTokens
	}
	return c.config.ReserveTokens
}

// KeepRecentTokens returns the effective keep-recent token budget.
func (c *Compactor) KeepRecentTokens() int {
	return c.effectiveKeepRecentTokens()
}

func (c *Compactor) effectiveKeepRecentTokens() int {
	if c == nil || c.config == nil || c.config.KeepRecentTokens <= 0 {
		return 0
	}

	keep := c.config.KeepRecentTokens
	if limit, _ := c.EffectiveTokenLimit(); limit > 0 {
		maxKeep := limit / 2
		if maxKeep > 0 && keep > maxKeep {
			keep = maxKeep
		}
	}

	return keep
}

// EffectiveTokenLimit returns the token limit for compaction and its source.
func (c *Compactor) EffectiveTokenLimit() (int, string) {
	if c == nil {
		return 0, "none"
	}
	if c.contextWindow > 0 {
		reserve := c.ReserveTokens()
		limit := c.contextWindow - reserve
		if limit > 0 {
			return limit, "context_window"
		}
	}
	if c.config != nil && c.config.MaxTokens > 0 {
		return c.config.MaxTokens, "max_tokens"
	}
	return 0, "none"
}

// EstimateTokens provides a rough estimation of token count.
func (c *Compactor) EstimateTokens(messages []agentctx.AgentMessage) int {
	totalTokens := 0
	for _, msg := range messages {
		if !msg.IsAgentVisible() {
			continue
		}
		totalTokens += estimateMessageTokens(msg)
	}
	return totalTokens
}

func estimateMessageTokens(msg agentctx.AgentMessage) int {
	if !msg.IsAgentVisible() {
		return 0
	}

	charCount := 0
	for _, block := range msg.Content {
		switch b := block.(type) {
		case agentctx.TextContent:
			charCount += len(b.Text)
		case agentctx.ThinkingContent:
			charCount += len(b.Thinking)
		case agentctx.ToolCallContent:
			charCount += len(b.Name)
			if b.Arguments != nil {
				if argBytes, err := json.Marshal(b.Arguments); err == nil {
					charCount += len(argBytes)
				}
			}
		case agentctx.ImageContent:
			// Roughly estimate images as 1200 tokens (4800 chars).
			charCount += 4800
		}
	}
	if charCount == 0 {
		charCount = len(msg.ExtractText())
	}
	if charCount == 0 {
		return 0
	}
	return int(math.Ceil(float64(charCount) / 4.0))
}

// Compact compacts context by summarizing old messages using AgentContext.
// This method implements the context.Compactor interface.
// goCtx carries trace context (trace buf + span) so LLM calls within
// compaction are properly traced.
func (c *Compactor) Compact(goCtx context.Context, ctx *agentctx.AgentContext) (*agentctx.CompactionResult, error) {
	if len(ctx.RecentMessages) == 0 {
		return &agentctx.CompactionResult{
			TokensBefore: 0,
			TokensAfter:  0,
		}, nil
	}

	// Compact is purely an execution method. The decision (including the
	// LLM-decides askLLM gate) lives in ShouldCompact.

	tokensBefore := ctx.EstimateTokens()

	keepRecentTokens := c.calculateKeepRecentBudget()

	oldMessages, recentMessages := splitMessagesByTokenBudget(ctx.RecentMessages, keepRecentTokens)
	if len(oldMessages) == 0 {
		// Token estimation says all messages fit within budget, but if we have
		// many messages the estimation is likely inaccurate (rough char/4
		// heuristic). Force a split when message count is high.
		const forceSplitMinMessages = 50
		if len(ctx.RecentMessages) > forceSplitMinMessages {
			keepCount := max(10, int(float64(len(ctx.RecentMessages))*0.3))
			splitIndex := len(ctx.RecentMessages) - keepCount
			oldMessages = ctx.RecentMessages[:splitIndex]
			recentMessages = ctx.RecentMessages[splitIndex:]
			slog.Info("[Compact] Forced split: token budget covered all messages but count exceeds threshold",
				"count", len(ctx.RecentMessages),
				"keepCount", keepCount,
				"keepTokens", keepRecentTokens,
				"forceSplitMin", forceSplitMinMessages)
		} else {
			return &agentctx.CompactionResult{
				TokensBefore: tokensBefore,
				TokensAfter:  tokensBefore,
			}, nil
		}
	}
	slog.Info("[Compact] Compressing messages",
		"count", len(ctx.RecentMessages),
		"keepTokens", keepRecentTokens,
		"threshold", c.CalculateDynamicThreshold(),
		"contextWindow", c.contextWindow)

	// Generate summary of old messages (with previous summary for incremental update)
	summary, err := c.GenerateSummary(goCtx, oldMessages, ctx.SystemPrompt, c.agentContextPrefix, ctx.Tools)
	if err != nil {
		return nil, fmt.Errorf("failed to generate summary: %w", err)
	}

	slog.Info("[Compact] Generated summary", "chars", len(summary))

	// Ensure tool_call and tool_result pairing is preserved
	if c.config.GracePeriod > 0 {
		recentMessages = c.ensureToolCallPairingWithGrace(oldMessages, recentMessages)
	} else {
		recentMessages = ensureToolCallPairing(oldMessages, recentMessages)
	}

	// Archive old messages so the agent can access them via read/grep later.
	archivePath := saveArchivedMessages(c.sessionDir, oldMessages)

	// Create new recent messages with summary, including archive path note.
	// The archive note is placed BEFORE the summary so the agent sees it first
	// and is more likely to use it proactively.
	summaryText := summary
	if archivePath != "" {
		summaryText = fmt.Sprintf(archiveNoteTemplate, archivePath) + "\n\n" + summary
	}
	newRecentMessages := []agentctx.AgentMessage{
		agentctx.NewCompactionSummaryMessage(summaryText),
	}

	recentMessages = compactToolResultsInRecent(recentMessages, c.config.ToolCallCutoff)
	recentMessages = cleanOldRuntimeState(recentMessages)
	newRecentMessages = append(newRecentMessages, recentMessages...)
	messagesBefore := len(ctx.RecentMessages)

	// Update AgentContext directly
	ctx.RecentMessages = newRecentMessages

	tokensAfter := ctx.EstimateTokens()
	messagesAfter := len(newRecentMessages)
	slog.Info("[Compact] Compressed context", "messages", messagesAfter)

	// Reset tool-call counter after successful compaction.
	ctx.AgentState.ToolCallsSinceLastTrigger = 0
	c.llmDecideLastAskCount = 0

	// Append a post-compaction hint so the LLM knows to reload skills and
	// design docs lost during compaction, and must acknowledge it before
	// making tool calls.
	AppendCompactionHint(ctx)

	return &agentctx.CompactionResult{
		Summary:        summary,
		TokensBefore:   tokensBefore,
		TokensAfter:    tokensAfter,
		MessagesBefore: messagesBefore,
		MessagesAfter:  messagesAfter,
		Type:           "major",
	}, nil
}

// cleanOldRuntimeState removes all but the last runtime_state message from the
// given slice. During compaction, older runtime_state snapshots are stale — only
// the most recent one carries useful telemetry. Cleaning them unconditionally
// keeps pkg/compact independent of cache mode logic.
func cleanOldRuntimeState(messages []agentctx.AgentMessage) []agentctx.AgentMessage {
	lastIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Metadata != nil && messages[i].Metadata.Kind == "runtime_state" {
			lastIdx = i
			break
		}
	}

	if lastIdx == -1 {
		return messages
	}

	var result []agentctx.AgentMessage
	for i, msg := range messages {
		if msg.Metadata != nil && msg.Metadata.Kind == "runtime_state" && i != lastIdx {
			continue
		}
		result = append(result, msg)
	}
	return result
}

// archiveNoteTemplate is prepended to the compaction summary so the agent knows
// where to find the full pre-compaction conversation. It uses directive language
// to encourage proactive recovery of lost context.
const archiveNoteTemplate = "<critical>\n" +
	"The full conversation before this summary is archived at `%s`.\n" +
	"This summary may omit important details — analysis results, intermediate findings, discussion context.\n" +
	"If anything seems incomplete or you are unsure what was discussed earlier, read this file (use the read or grep tool) BEFORE asking the user.\n" +
	"</critical>"

// saveArchivedMessages writes old messages removed during compaction to a
// sequential JSONL file under <sessionDir>/compactions/archived_NNNNN.jsonl.
// Returns the absolute path, or "" if sessionDir is empty or no messages.
func saveArchivedMessages(sessionDir string, messages []agentctx.AgentMessage) string {
	if sessionDir == "" || len(messages) == 0 {
		return ""
	}
	compactionsDir := filepath.Join(sessionDir, "compactions")
	entries, err := os.ReadDir(compactionsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("[Compact] Failed to read compactions dir for archiving", "error", err)
			return ""
		}
	}
	count := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "archived_") {
			count++
		}
	}
	name := fmt.Sprintf("archived_%05d.jsonl", count+1)
	archivePath := filepath.Join(compactionsDir, name)

	if err := os.MkdirAll(compactionsDir, 0755); err != nil {
		slog.Warn("[Compact] Failed to create compactions dir for archiving", "error", err)
		return ""
	}

	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	for _, msg := range messages {
		if err := enc.Encode(msg); err != nil {
			slog.Warn("[Compact] Failed to encode archived message", "error", err)
			return ""
		}
	}
	if err := os.WriteFile(archivePath, []byte(buf.String()), 0644); err != nil {
		slog.Warn("[Compact] Failed to write archived messages", "path", archivePath, "error", err)
		return ""
	}

	slog.Info("[Compact] Archived old messages", "path", archivePath, "count", len(messages))
	return archivePath
}

// ShouldCompact determines if context should be compressed.
// In LLMDecide mode, uses soft/hard thresholds + tool-call intervals.
// In classic mode, uses the dynamic token threshold.
func (c *Compactor) ShouldCompact(ctx context.Context, agentCtx *agentctx.AgentContext) bool {
	if !c.config.AutoCompact {
		return false
	}

	// LLMDecide is always enabled (set unconditionally in rpc_setup.go).
	return c.shouldCompactLLMDecide(ctx, agentCtx)
}

// shouldCompactLLMDecide implements the LLM-decides threshold check.
// When an interval is reached, it asks the LLM whether to compact;
// on error it falls back to compacting.
func (c *Compactor) shouldCompactLLMDecide(ctx context.Context, agentCtx *agentctx.AgentContext) bool {
	tokens := agentCtx.EstimateTokens()
	cfg := c.config.LLMDecide

	if tokens >= cfg.HardLimit {
		traceevent.Log(ctx, traceevent.CategoryEvent, "compact_llm_decide_check",
			traceevent.Field{Key: "decision", Value: true},
			traceevent.Field{Key: "reason", Value: "hard_limit"},
			traceevent.Field{Key: "tokens", Value: tokens},
			traceevent.Field{Key: "hard_limit", Value: cfg.HardLimit},
		)
		return true
	}
	if tokens < cfg.SoftThreshold {
		return false
	}

	interval := c.llmDecideInterval(tokens)
	tier := "low"
	switch {
	case tokens >= cfg.TierHigh:
		tier = "high"
	case tokens >= cfg.TierMedium:
		tier = "medium"
	}

	currentCount := agentCtx.AgentState.ToolCallsSinceLastTrigger

	// Don't re-ask until a full interval has elapsed since the last ask.
	// This prevents asking every turn after a "no".
	if currentCount-c.llmDecideLastAskCount < interval {
		return false
	}

	// Interval reached — ask the LLM whether to compact.
	// The askLLM span (compact_llm_decide_ask) records cache/token details.
	ask := c.askFunc
	if ask == nil {
		ask = c.askLLM
	}
	shouldDo, err := ask(ctx, agentCtx, tokens)

	decision := true
	reason := "ask_yes"
	if err != nil {
		slog.Warn("[Compact] LLM-decide ask failed, compacting as fallback", "error", err)
		reason = "ask_fallback"
	} else if !shouldDo {
		decision = false
		reason = "ask_no"
		slog.Info("[Compact] LLM decided not to compact",
			"tokens", tokens,
			"budget_pct", fmt.Sprintf("%.0f%%", float64(tokens)/float64(cfg.HardLimit)*100))
	} else {
		slog.Info("[Compact] LLM decided to compact",
			"tokens", tokens,
			"budget_pct", fmt.Sprintf("%.0f%%", float64(tokens)/float64(cfg.HardLimit)*100))
	}

	// Record the counter when the last ask happened.
	// llmDecideLastAskCount prevents re-asking until a full interval has elapsed.
	c.llmDecideLastAskCount = currentCount

	traceevent.Log(ctx, traceevent.CategoryEvent, "compact_llm_decide_check",
		traceevent.Field{Key: "decision", Value: decision},
		traceevent.Field{Key: "reason", Value: reason},
		traceevent.Field{Key: "tokens", Value: tokens},
		traceevent.Field{Key: "tier", Value: tier},
		traceevent.Field{Key: "interval", Value: interval},
	)
	return decision
}

func (c *Compactor) llmDecideInterval(tokens int) int {
	cfg := c.config.LLMDecide
	switch {
	case tokens >= cfg.TierHigh:
		return cfg.IntervalHigh
	case tokens >= cfg.TierMedium:
		return cfg.IntervalMedium
	default:
		return cfg.IntervalLow
	}
}

// buildCacheFriendlyLLMContext builds an LLM request whose prefix matches a
// normal agent turn, maximising provider prefix-cache hits. Used by both
// askLLM and GenerateSummary.
//
// Message ordering (mirrors the agent loop):
//
//	[system_prompt]
//	[contextPrefix as user message]   ← skills + AGENTS.md, only if non-empty
//	[...conversation messages...]
//	[trailingInstruction]             ← ask question or summarisation prompt
func buildCacheFriendlyLLMContext(
	messages []agentctx.AgentMessage,
	systemPrompt string,
	contextPrefix string,
	tools []agentctx.Tool,
	trailingInstruction string,
	thinkingLevel string,
) llm.LLMContext {
	llmMessages := agentctx.ConvertMessagesToLLM(messages)

	if strings.TrimSpace(contextPrefix) != "" {
		llmMessages = append([]llm.LLMMessage{{
			Role:    "user",
			Content: contextPrefix,
		}}, llmMessages...)
	}

	llmMessages = append(llmMessages, llm.LLMMessage{
		Role:    "user",
		Content: trailingInstruction,
	})

	return llm.LLMContext{
		SystemPrompt:  systemPrompt,
		Messages:      llmMessages,
		Tools:         agentctx.ConvertToolsToLLM(tools),
		ThinkingLevel: thinkingLevel,
	}
}

// askLLM sends a lightweight yes/no question to the LLM, reusing the main
// conversation prefix for cache efficiency. Returns true if the LLM says yes.
func (c *Compactor) askLLM(ctx context.Context, agentCtx *agentctx.AgentContext, tokens int) (bool, error) {
	span := traceevent.StartSpan(ctx, "compact_llm_decide_ask", traceevent.CategoryLLM)
	defer span.End()

	if c.askPrompt == "" {
		c.askPrompt = prompt.CompactCheckPrompt()
	}

	cfg := c.config.LLMDecide
	budgetPct := fmt.Sprintf("%d%% (%d / %d tokens)", tokens*100/cfg.HardLimit, tokens, cfg.HardLimit)
	askContent := fmt.Sprintf(c.askPrompt, budgetPct)

	span.AddField("tokens", tokens)
	span.AddField("budget_pct", budgetPct)

	llmCtx := buildCacheFriendlyLLMContext(
		agentCtx.RecentMessages,
		agentCtx.SystemPrompt,
		c.agentContextPrefix,
		agentCtx.Tools,
		askContent,
		c.thinkingLevel,
	)

	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	stream := llm.StreamLLM(callCtx, c.model, llmCtx, c.apiKey, 60*time.Second)

	var response strings.Builder
	var thinking strings.Builder
	for event := range stream.Iterator(callCtx) {
		if event.Done {
			break
		}
		switch e := event.Value.(type) {
		case llm.LLMTextDeltaEvent:
			response.WriteString(e.Delta)
		case llm.LLMThinkingDeltaEvent:
			thinking.WriteString(e.Delta)
		case llm.LLMDoneEvent:
			span.AddField("input_tokens", e.Usage.InputTokens)
			span.AddField("output_tokens", e.Usage.OutputTokens)
			span.AddField("total_tokens", e.Usage.TotalTokens)
			if e.Usage.PromptTokensDetails != nil {
				span.AddField("cache_read", e.Usage.PromptTokensDetails.CachedTokens)
			}
		case llm.LLMErrorEvent:
			span.AddField("error", e.Error.Error())
			return false, e.Error
		}
	}

	// Fall back to reasoning_content if text response is empty (same model
	// behavior as GenerateSummary).
	answerText := response.String()
	if strings.TrimSpace(answerText) == "" && thinking.Len() > 0 {
		answerText = thinking.String()
		span.AddField("used_thinking_fallback", true)
	}

	// Parse only the first line to avoid multi-line pollution.
	answer := strings.ToLower(strings.TrimSpace(answerText))
	if idx := strings.IndexByte(answer, '\n'); idx >= 0 {
		answer = answer[:idx]
	}
	answer = strings.TrimSpace(answer)
	confirmed := strings.Contains(answer, "confirm")

	span.AddField("response", answerText)
	span.AddField("decision", confirmed)

	return confirmed, nil
}

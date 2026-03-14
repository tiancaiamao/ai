package context

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	LLMContextDir = "llm-context"
	OverviewFile  = "overview.md"
	DetailDir     = "detail"

	// Update tracking thresholds
	baseRoundsBeforeReminder = 10 // Default base threshold for reminders
	MaxRoundsWithoutUpdate   = 10 // Maximum rounds without update before reminder (legacy)
	minRoundsBeforeCheck     = 3  // Minimum rounds before checking for update
)

// ContextMeta contains metadata about the current context state.
type ContextMeta struct {
	TokensUsed        int     `json:"tokens_used"`
	TokensMax         int     `json:"tokens_max"`
	TokensPercent     float64 `json:"tokens_percent"`
	MessagesInHistory int     `json:"messages_in_history"`
	LLMContextSize    int     `json:"llm_context_size"` // bytes
}

// LLMContextWriter defines the interface for writing LLM context.
// This allows tools to update the context without depending on the full LLMContext type.
type LLMContextWriter interface {
	WriteContent(content string) error
}

// LLMContext manages the agent's llm context (overview.md).
// It provides caching based on file modification time and update tracking.
type LLMContext struct {
	mu sync.RWMutex

	// Paths
	sessionDir   string
	overviewPath string
	detailPath   string

	// Cache
	overviewContent string
	overviewModTime time.Time

	// Meta
	tokensUsed    int
	tokensMax     int
	messagesCount int

	// Update tracking
	lastUpdateTime        time.Time
	lastCheckTime         time.Time
	roundsSinceUpdate     int
	silentRoundsRemaining int  // Rounds to skip reminder after update
	wasRemindedLastRound  bool // Was reminder injected in the last round?

	// Decision tracking - for separate reminder when LLM updates overview but doesn't call llm_context_decision.
	updatedOverviewThisTurn   bool // LLM updated overview in the current turn
	decisionNeededThisTurn    bool // runtime_state.action_required != none for the current turn
	pendingDecisionReminder   bool // Waiting for llm_context_decision after overview update
	roundsSinceDecisionNeeded int  // Rounds since decision became pending
	staleToolOutputs          int  // Number of stale tool outputs (updated from runtime)

	// Update statistics for adaptive reminder frequency
	totalUpdates      int // Total number of updates
	autonomousUpdates int // Updates without prompt (LLM self-initiated)
	promptedUpdates   int // Updates after prompt
	nextReminderRound int // Dynamic threshold for next reminder (5-30)
}

// NewLLMContext creates a new LLMContext for the given session directory.
func NewLLMContext(sessionDir string) *LLMContext {
	return &LLMContext{
		sessionDir:        sessionDir,
		overviewPath:      filepath.Join(sessionDir, LLMContextDir, OverviewFile),
		detailPath:        filepath.Join(sessionDir, LLMContextDir, DetailDir),
		nextReminderRound: baseRoundsBeforeReminder, // Default threshold
	}
}

// GetOverviewTemplate returns the default template for overview.md with the given path.
func GetOverviewTemplate(overviewPath, DetailDir string) string {
	return fmt.Sprintf(`# LLM Context

<!--
这是你的外部记忆。
使用 llm_context_update tool 更新此文件：%s

这个文件的内容会：
1. 在你调用 llm_context_update 工具后，通过 tool output 留在上下文窗口中
2. 在 compact 后被注入到 prompt 中恢复记忆

这是 YOUR memory。你控制你看到的内容。
-->

## 当前任务
<!-- 用户让你做什么？当前进度？ -->


## 关键决策
<!-- 你做过什么重要决定？为什么？ -->


## 已知信息
<!-- 项目结构、技术栈、关键文件等 -->


## 待解决
<!-- 待处理的问题或阻塞项 -->


<!--
提示：
- 需要保存详细内容时，写入 %s 目录
- 使用 llm_context_update tool 更新此文件
-->
`, overviewPath, DetailDir)
}

// ensureLLMContext creates the llm-context directory structure if needed.
func (wm *LLMContext) ensureLLMContext() error {
	wmDir := filepath.Join(wm.sessionDir, LLMContextDir)
	if err := os.MkdirAll(wmDir, 0755); err != nil {
		return fmt.Errorf("failed to create llm-context directory: %w", err)
	}

	DetailDir := filepath.Join(wmDir, DetailDir)
	if err := os.MkdirAll(DetailDir, 0755); err != nil {
		return fmt.Errorf("failed to create detail directory: %w", err)
	}

	if _, err := os.Stat(wm.overviewPath); os.IsNotExist(err) {
		template := GetOverviewTemplate(wm.overviewPath, wm.detailPath)
		if err := os.WriteFile(wm.overviewPath, []byte(template), 0644); err != nil {
			return fmt.Errorf("failed to write overview template: %w", err)
		}
	}

	return nil
}

// Load loads the overview.md content with mtime-based caching.
// It also checks if a reminder about updating llm context should be shown.
func (wm *LLMContext) Load() (string, error) {
	content, err := wm.loadContent()
	if err != nil {
		return "", err
	}

	// Check if we need to show a reminder
	needsUpdate, reminder := wm.checkUpdateNeeded()
	if needsUpdate {
		content = content + reminder
	}

	return content, nil
}

// loadContent loads the overview.md content with mtime-based caching.
// This is an internal method that only handles file loading.
func (wm *LLMContext) loadContent() (string, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	// Ensure directory exists
	if err := wm.ensureLLMContext(); err != nil {
		return "", err
	}

	info, err := os.Stat(wm.overviewPath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist, return template
			return GetOverviewTemplate(wm.overviewPath, wm.detailPath), nil
		}
		return "", err
	}

	// Check if cache is still valid
	if info.ModTime().Equal(wm.overviewModTime) && wm.overviewContent != "" {
		return wm.overviewContent, nil
	}

	// Read file
	content, err := os.ReadFile(wm.overviewPath)
	if err != nil {
		return "", err
	}

	wm.overviewContent = string(content)
	wm.overviewModTime = info.ModTime()
	return wm.overviewContent, nil
}

// GetPath returns the path to overview.md.
func (wm *LLMContext) GetPath() string {
	return wm.overviewPath
}

// GetDetailDir returns the path to the detail directory.
func (wm *LLMContext) GetDetailDir() string {
	return wm.detailPath
}

// GetSessionDir returns the session directory path.
func (wm *LLMContext) GetSessionDir() string {
	return wm.sessionDir
}

// UpdateMeta updates context metadata.
func (wm *LLMContext) UpdateMeta(tokensUsed, tokensMax, messagesCount int) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	wm.tokensUsed = tokensUsed
	wm.tokensMax = tokensMax
	wm.messagesCount = messagesCount
}

// GetMeta returns the current context metadata.
func (wm *LLMContext) GetMeta() ContextMeta {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	// Calculate llm context size
	var wmSize int
	if info, err := os.Stat(wm.overviewPath); err == nil {
		wmSize = int(info.Size())
	}

	// Use default context window if not set
	tokensMax := wm.tokensMax
	if tokensMax <= 0 {
		tokensMax = 128000 // default context window
	}

	// Calculate percentage
	var percent float64
	if tokensMax > 0 && wm.tokensUsed > 0 {
		percent = float64(wm.tokensUsed) / float64(tokensMax) * 100
	}

	// Use message count from agent context if available
	messagesCount := wm.messagesCount

	return ContextMeta{
		TokensUsed:        wm.tokensUsed,
		TokensMax:         tokensMax,
		TokensPercent:     percent,
		MessagesInHistory: messagesCount,
		LLMContextSize:    wmSize,
	}
}

// InvalidateCache clears the cached content, forcing a reload on next Load().
func (wm *LLMContext) InvalidateCache() {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	wm.overviewContent = ""
	wm.overviewModTime = time.Time{}
}

// MarkUpdated marks that llm context has been updated.
// This resets the roundsSinceUpdate counter and sets a silent period.
// silentRounds: number of rounds to skip reminder (default 5 if <= 0)
// autonomous: true if update was self-initiated (not prompted), false if after prompt
func (wm *LLMContext) MarkUpdated(silentRounds int, autonomous bool) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	wm.lastUpdateTime = time.Now()
	wm.roundsSinceUpdate = 0

	// Set silent period
	if silentRounds <= 0 {
		silentRounds = 5 // Default silent period
	}
	wm.silentRoundsRemaining = silentRounds

	// Update statistics
	if autonomous {
		wm.autonomousUpdates++
	} else {
		wm.promptedUpdates++
	}
	wm.totalUpdates++

	// Adjust threshold based on update type
	wm.adjustThreshold(autonomous)
}

// MarkUpdatedAfterToolCall detects if this update was autonomous or prompted.
// This should be called when a write tool call updates llm context.
func (wm *LLMContext) MarkUpdatedAfterToolCall(silentRounds int) {
	wm.mu.Lock()
	wasReminded := wm.wasRemindedLastRound
	wm.mu.Unlock()

	// If we were reminded, this is a prompted update
	// Otherwise, it's autonomous
	wm.MarkUpdated(silentRounds, !wasReminded)
}

// IncrementRound increments the round counter.
// This should be called from the agent loop on each LLM request.
// Call MarkUpdated() when the LLM actually updates llm context.
func (wm *LLMContext) IncrementRound() {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	// Skip increment if in silent period
	if wm.silentRoundsRemaining > 0 {
		wm.silentRoundsRemaining--
		return
	}

	wm.roundsSinceUpdate++
}

// GetRoundsSinceUpdate returns the number of rounds since the last update.
func (wm *LLMContext) GetRoundsSinceUpdate() int {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return wm.roundsSinceUpdate
}

// checkUpdateNeeded checks if a reminder should be shown about updating llm context.
// Returns (shouldShowReminder, reminderMessage).
// NOTE: This method does NOT auto-increment the round counter.
// Round tracking should be done via IncrementRound() from the agent loop.
func (wm *LLMContext) checkUpdateNeeded() (bool, string) {
	wm.mu.Lock()
	rounds := wm.roundsSinceUpdate
	threshold := wm.nextReminderRound
	wm.mu.Unlock()

	// Don't check if we haven't tracked any rounds yet
	if rounds <= 0 {
		return false, ""
	}

	// Don't check before minimum rounds
	if rounds < minRoundsBeforeCheck {
		return false, ""
	}

	// Check if we need to remind based on dynamic threshold
	if rounds >= threshold {
		meta := wm.GetMeta()
		return true, wm.buildReminderHTML(meta)
	}

	return false, ""
}

// buildReminderHTML builds an HTML comment reminder (appended to llm context content).
func (wm *LLMContext) buildReminderHTML(meta ContextMeta) string {
	consciousness := wm.GetUpdateConsciousness()
	consciousnessPercent := int(consciousness * 100)

	return fmt.Sprintf(`

<!--
⚠️ WORKING MEMORY UPDATE NEEDED

你已经连续 %d 轮没有调用 llm_context_update 了（动态阈值：%d 轮）。
当前上下文状态:
- Token 使用: %.0f%% (%d / %d)
- 历史消息: %d 条
- LLM Context 大小: %.2f KB

💡 自主更新奖励机制：
- 当前自觉度：%d%%（%d/%d 次更新是自主的）
- 你更新越自觉，提醒频率越低

  如果继续保持自主更新（提醒前主动更新）：
  - 下次提醒阈值会提高 → 你可以有更长的"忘记提醒"时间
  - 阈值范围：5-30 轮
  
  如果总是需要提醒才更新：
  - 下次提醒阈值会降低 → 提醒会更频繁

建议操作:
1. 总结已完成的任务，归档到 %s
2. 更新"当前任务"状态和进度
3. 删除过时信息，保留最近决策
4. 将详细讨论移到 detail/ 目录

使用 llm_context_update tool 更新: %s
-->`,
		wm.roundsSinceUpdate,
		wm.nextReminderRound,
		meta.TokensPercent,
		meta.TokensUsed,
		meta.TokensMax,
		meta.MessagesInHistory,
		float64(meta.LLMContextSize)/1024,
		consciousnessPercent,
		wm.autonomousUpdates,
		wm.totalUpdates,
		wm.detailPath,
		wm.overviewPath)
}

// NeedsReminderMessage checks if a reminder message should be injected.
// This is a separate check from checkUpdateNeeded() to allow for different thresholds.
// Reminder is shown when LLM hasn't updated overview.md for too many rounds.
func (wm *LLMContext) NeedsReminderMessage() bool {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	// Use dynamic threshold instead of fixed maxRoundsWithoutUpdate
	return wm.roundsSinceUpdate >= wm.nextReminderRound
}

// GetReminderUserMessage builds a user message reminder to inject into the conversation.
// The message is clearly marked as agent-generated, not from a real user.
// This reminder is for updating overview.md - the llm_context_decision is handled separately.
func (wm *LLMContext) GetReminderUserMessage() string {
	meta := wm.GetMeta()

	wm.mu.RLock()
	rounds := wm.roundsSinceUpdate
	wm.mu.RUnlock()

	return fmt.Sprintf(`<agent:remind comment="system message by agent, not from real user">

💡 Remember to update your llm context to track progress.

<context_meta>
tokens_used: %d
tokens_max: %d
tokens_percent: %.0f%%
messages_in_history: %d
rounds_since_update: %d
</context_meta>

Resident prompt path: %s
Detail directory: %s

To update: use the llm_context_update tool to modify the llm context.
This reminder will stop appearing once you update your llm context.`, meta.TokensUsed, meta.TokensMax, meta.TokensPercent, meta.MessagesInHistory, rounds, wm.overviewPath, wm.detailPath)
}

// SaveCompactionSummary saves a compaction summary to the detail directory.
// This allows recall_memory to search through past compaction summaries.
func (wm *LLMContext) SaveCompactionSummary(summary string) (string, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	// Ensure detail directory exists
	if err := os.MkdirAll(wm.detailPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create detail directory: %w", err)
	}

	// Generate filename with timestamp
	timestamp := time.Now().Format("2006-01-02-150405")
	filename := fmt.Sprintf("compaction-%s.md", timestamp)
	fullpath := filepath.Join(wm.detailPath, filename)

	// Write summary with metadata header
	content := fmt.Sprintf(`# Compaction Summary

<!--
META:
- created: %s
- type: compaction
-->

%s
`, time.Now().Format(time.RFC3339), summary)

	if err := os.WriteFile(fullpath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write compaction summary: %w", err)
	}

	slog.Info("[LLMContext] Saved compaction summary", "path", fullpath)

	// Return relative path from session directory
	return filepath.Join("llm-context", "detail", filename), nil
}

// WriteContent writes the given content to the overview.md file.
// This is used to restore llm context from compaction summaries.
func (wm *LLMContext) WriteContent(content string) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	// Ensure directory exists
	if err := wm.ensureLLMContext(); err != nil {
		return err
	}

	// Write content
	if err := os.WriteFile(wm.overviewPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write overview.md: %w", err)
	}

	// Update cache
	wm.overviewContent = content
	if info, err := os.Stat(wm.overviewPath); err == nil {
		wm.overviewModTime = info.ModTime()
	}

	slog.Info("[LLMContext] Updated overview.md from compaction summary", "path", wm.overviewPath)
	return nil
}

// adjustThreshold dynamically adjusts the nextReminderRound threshold based on update behavior.
// autonomous: true for self-initiated updates (increase threshold), false for prompted updates (decrease)
func (wm *LLMContext) adjustThreshold(autonomous bool) {
	if wm.totalUpdates < 2 {
		// Not enough data yet, use base threshold
		wm.nextReminderRound = baseRoundsBeforeReminder
		return
	}

	// Calculate consciousness (autonomous update ratio)
	consciousness := float64(wm.autonomousUpdates) / float64(wm.totalUpdates)

	// Determine delta based on update type and consciousness
	delta := 0
	if autonomous {
		// Autonomous update: increase threshold
		if consciousness > 0.7 {
			delta = 3 // Highly conscious: big reward
		} else if consciousness > 0.4 {
			delta = 2 // Moderately conscious
		} else {
			delta = 1 // Low consciousness: small reward
		}
	} else {
		// Prompted update: decrease threshold
		if consciousness > 0.6 {
			delta = -1 // Still fairly conscious: small penalty
		} else if consciousness > 0.3 {
			delta = -2 // Moderate penalty
		} else {
			delta = -3 // Low consciousness: big penalty
		}
	}

	// Apply delta and clamp to range [5, 30]
	wm.nextReminderRound += delta
	if wm.nextReminderRound < 5 {
		wm.nextReminderRound = 5
	}
	if wm.nextReminderRound > 30 {
		wm.nextReminderRound = 30

		slog.Info("[LLMContext] Adjusted reminder threshold",
			"autonomous", autonomous,
			"consciousness", consciousness,
			"delta", delta,
			"new_threshold", wm.nextReminderRound,
			"autonomous_updates", wm.autonomousUpdates,
			"prompted_updates", wm.promptedUpdates)
	}
}

// GetUpdateConsciousness returns the ratio of autonomous updates (0.0-1.0)
func (wm *LLMContext) GetUpdateConsciousness() float64 {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	if wm.totalUpdates == 0 {
		return 0.0
	}
	return float64(wm.autonomousUpdates) / float64(wm.totalUpdates)
}

// UpdateStats contains statistics about llm_context_update tool calls.
type UpdateStats struct {
	Total       int
	Autonomous  int
	Prompted    int
	Score       string // "excellent", "good", "needs_improvement", "no_data"
	ConsciousPct int   // percentage 0-100
}

// GetUpdateStats returns statistics about llm_context_update tool calls.
func (wm *LLMContext) GetUpdateStats() UpdateStats {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	stats := UpdateStats{
		Total:      wm.totalUpdates,
		Autonomous: wm.autonomousUpdates,
		Prompted:   wm.promptedUpdates,
	}

	if wm.totalUpdates > 0 {
		stats.ConsciousPct = int(float64(wm.autonomousUpdates) * 100 / float64(wm.totalUpdates))
		// Score based on autonomous percentage
		switch {
		case stats.ConsciousPct >= 80:
			stats.Score = "excellent"
		case stats.ConsciousPct >= 60:
			stats.Score = "good"
		default:
			stats.Score = "needs_improvement"
		}
	} else {
		stats.Score = "no_data"
	}

	return stats
}

// GetNextReminderRound returns the current dynamic threshold
func (wm *LLMContext) GetNextReminderRound() int {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return wm.nextReminderRound
}

// SetWasReminded marks that a reminder was injected in this round.
// This helps track whether the next update is autonomous or prompted.
func (wm *LLMContext) SetWasReminded() {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	wm.wasRemindedLastRound = true
}

// ResetReminderFlag clears the "was reminded" flag after checking.
// This should be called after checking MarkUpdated().
func (wm *LLMContext) ResetReminderFlag() {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	wm.wasRemindedLastRound = false
}

// SetUpdatedOverview marks that LLM updated overview.md this round.
// This should be called when LLM writes to overview.md.
func (wm *LLMContext) SetUpdatedOverview() {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	wm.updatedOverviewThisTurn = true
}

// SetDecisionNeededThisTurn marks whether context management is required in this turn.
func (wm *LLMContext) SetDecisionNeededThisTurn(needed bool) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	wm.decisionNeededThisTurn = needed
}

// MarkDecisionMade marks that LLM called llm_context_decision tool.
// This resets the decision reminder counter.
func (wm *LLMContext) MarkDecisionMade() {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	wm.updatedOverviewThisTurn = false
	wm.decisionNeededThisTurn = false
	wm.pendingDecisionReminder = false
	wm.roundsSinceDecisionNeeded = 0
}

// NeedsDecisionReminder checks if a decision reminder should be shown.
// This is separate from overview update reminder - it triggers when:
// - LLM has updated overview.md
// - But hasn't called llm_context_decision tool
// - And decision is still needed (action_required != "none")
func (wm *LLMContext) NeedsDecisionReminder() bool {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return wm.pendingDecisionReminder && wm.roundsSinceDecisionNeeded >= 2
}

// AdvanceDecisionState advances per-turn decision reminder state at turn boundary.
func (wm *LLMContext) AdvanceDecisionState(decisionMadeThisTurn bool) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if decisionMadeThisTurn {
		wm.updatedOverviewThisTurn = false
		wm.decisionNeededThisTurn = false
		wm.pendingDecisionReminder = false
		wm.roundsSinceDecisionNeeded = 0
		return
	}

	if !wm.decisionNeededThisTurn {
		wm.pendingDecisionReminder = false
		wm.roundsSinceDecisionNeeded = 0
		wm.updatedOverviewThisTurn = false
		wm.decisionNeededThisTurn = false
		return
	}

	if wm.updatedOverviewThisTurn {
		// The turn that updates overview is the baseline turn; start counting from next turn.
		wm.pendingDecisionReminder = true
		wm.roundsSinceDecisionNeeded = 0
	} else if wm.pendingDecisionReminder {
		wm.roundsSinceDecisionNeeded++
	}

	wm.updatedOverviewThisTurn = false
	wm.decisionNeededThisTurn = false
}

// SetStaleToolCount sets the number of stale tool outputs (called from runtime).
func (wm *LLMContext) SetStaleToolCount(count int) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	wm.staleToolOutputs = count
}

// GetStaleToolCount returns the number of stale tool outputs.
func (wm *LLMContext) GetStaleToolCount() int {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return wm.staleToolOutputs
}

// GetDecisionReminderMessage returns a user message reminder for llm_context_decision.
func (wm *LLMContext) GetDecisionReminderMessage(availableToolIDs []string) string {
	meta := wm.GetMeta()
	staleCount := wm.GetStaleToolCount()

	// Build truncate_ids example from available (non-truncated) tool IDs
	var truncateIDsExample string
	if len(availableToolIDs) > 0 {
		// Show first few IDs as example, limit to 5 to avoid overwhelming
		limit := 5
		if len(availableToolIDs) < limit {
			limit = len(availableToolIDs)
		}
		exampleIDs := make([]string, limit)
		for i := 0; i < limit; i++ {
			exampleIDs[i] = availableToolIDs[i]
		}
		if len(availableToolIDs) > limit {
			truncateIDsExample = fmt.Sprintf(`"%s, ...%d more"`,
				strings.Join(exampleIDs, ", "), len(availableToolIDs)-limit)
		} else {
			truncateIDsExample = fmt.Sprintf(`"%s"`, strings.Join(exampleIDs, ", "))
		}
	}

	return fmt.Sprintf(`<agent:remind comment="system message by agent, not from real user">

💡 Context management required: tokens at %d%%, %d stale tool outputs.

<context_meta>
tokens_used: %d
tokens_max: %d
tokens_percent: %.0f%%
messages_in_history: %d
</context_meta>

Suggested: %s

HOW TO TRUNCATE (IMPORTANT):
1. Find IDs with stale="N" attribute: <agent:tool id="call_xxx" stale="5" />
2. **SKIP IDs with truncated="true"** - these are already truncated!
3. Batch clean: get 50-100 IDs at once
4. Pass as comma-separated string: truncate_ids: "call_abc, call_def, ..."

EXAMPLE (copy and modify):
decision: "truncate"
reasoning: "Cleaning up %d stale tool outputs"
truncate_ids: %s

⚠️ WARNING: Including already-truncated IDs will result in "0 truncated".`,
		int(meta.TokensPercent), staleCount,
		meta.TokensUsed, meta.TokensMax, meta.TokensPercent, meta.MessagesInHistory,
		getSuggestedAction(meta.TokensPercent, staleCount),
		staleCount, truncateIDsExample)
}

// getSuggestedAction returns suggested action based on token usage and stale outputs.
func getSuggestedAction(tokensPercent float64, staleCount int) string {
	// High priority: many stale outputs → TRUNCATE
	if staleCount > 20 {
		return "TRUNCATE (many stale outputs)"
	}
	if staleCount > 10 {
		return "TRUNCATE (several stale outputs)"
	}

	// Low stale count, just show token percentage
	return fmt.Sprintf("Token usage: %.0f%%", tokensPercent)
}

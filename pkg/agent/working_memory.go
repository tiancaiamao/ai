package agent

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	workingMemoryDir = "working-memory"
	overviewFile     = "overview.md"
	detailDir        = "detail"

	// Update tracking thresholds
	maxRoundsWithoutUpdate = 10 // Maximum rounds without update before reminder
	minRoundsBeforeCheck   = 3 // Minimum rounds before checking for update
)

// ContextMeta contains metadata about the current context state.
type ContextMeta struct {
	TokensUsed        int     `json:"tokens_used"`
	TokensMax         int     `json:"tokens_max"`
	TokensPercent     float64 `json:"tokens_percent"`
	MessagesInHistory int     `json:"messages_in_history"`
	WorkingMemorySize int     `json:"working_memory_size"` // bytes
}

// WorkingMemory manages the agent's working memory (overview.md).
// It provides caching based on file modification time and update tracking.
type WorkingMemory struct {
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
	lastUpdateTime    time.Time
	lastCheckTime     time.Time
	roundsSinceUpdate int
}

// NewWorkingMemory creates a new WorkingMemory for the given session directory.
func NewWorkingMemory(sessionDir string) *WorkingMemory {
	return &WorkingMemory{
		sessionDir:   sessionDir,
		overviewPath: filepath.Join(sessionDir, workingMemoryDir, overviewFile),
		detailPath:   filepath.Join(sessionDir, workingMemoryDir, detailDir),
	}
}

// GetOverviewTemplate returns the default template for overview.md with the given path.
func GetOverviewTemplate(overviewPath, detailDir string) string {
	return fmt.Sprintf(`# Working Memory

<!--
这是你的外部记忆。每次请求时，这个文件的内容会被加载到你的 prompt 中。
你自己决定记住什么、丢弃什么。

使用 write tool 更新此文件：%s
下次请求时，你会看到自己写的内容。

这是 YOUR memory。你控制你看到的内容。

⚠️ 路径规则（非常重要）：
- 以 system prompt 中 Working Memory 的 Path / Detail dir 为准
- 不要使用相对于当前工作目录的路径（例如 working-memory/overview.md）
-->

## 上下文管理指南

每次请求会附带 <context_meta> 元信息：
- tokens_used: 已使用的 token 数
- tokens_max: 最大 token 数  
- tokens_percent: 使用百分比
- messages_in_history: 历史消息数量
- working_memory_size: working memory 大小（字节）

### 上下文压缩触发条件

当 tokens_percent >= 70%% 时，你应该主动压缩上下文：

1. **总结历史对话**：将已完成的任务、已解决的问题归档到 detail 目录
2. **精简 overview.md**：只保留当前任务、关键决策、待解决问题
3. **使用 write tool** 更新此文件，系统会在下次请求时使用压缩后的内容

压缩示例：
- 将详细的调试过程移到 detail/debug-xxx.md
- 将已完成的任务从"当前任务"移到"已完成"
- 删除不再需要的临时信息

## 当前任务
<!-- 用户让你做什么？当前进度？ -->


## 关键决策
<!-- 你做过什么重要决定？为什么？ -->


## 已知信息
<!-- 项目结构、技术栈、关键文件等 -->


## 待解决
<!-- 待处理的问题或阻塞项 -->


## 最近操作
<!-- 最近几步做了什么（可选，用于快速回顾） -->


<!--
提示：
- 需要保存详细内容时，写入 %s 目录
- 路径优先使用 system prompt 给出的绝对路径
-->
`, overviewPath, detailDir)
}

// ensureWorkingMemory creates the working-memory directory structure if needed.
func (wm *WorkingMemory) ensureWorkingMemory() error {
	wmDir := filepath.Join(wm.sessionDir, workingMemoryDir)
	if err := os.MkdirAll(wmDir, 0755); err != nil {
		return fmt.Errorf("failed to create working-memory directory: %w", err)
	}

	detailDir := filepath.Join(wmDir, detailDir)
	if err := os.MkdirAll(detailDir, 0755); err != nil {
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
// It also checks if a reminder about updating working memory should be shown.
func (wm *WorkingMemory) Load() (string, error) {
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
func (wm *WorkingMemory) loadContent() (string, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	// Ensure directory exists
	if err := wm.ensureWorkingMemory(); err != nil {
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
func (wm *WorkingMemory) GetPath() string {
	return wm.overviewPath
}

// GetDetailDir returns the path to the detail directory.
func (wm *WorkingMemory) GetDetailDir() string {
	return wm.detailPath
}

// UpdateMeta updates the context metadata.
func (wm *WorkingMemory) UpdateMeta(tokensUsed, tokensMax, messagesCount int) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	wm.tokensUsed = tokensUsed
	wm.tokensMax = tokensMax
	wm.messagesCount = messagesCount
}

// GetMeta returns the current context metadata.
func (wm *WorkingMemory) GetMeta() ContextMeta {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	// Calculate working memory size
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
		WorkingMemorySize: wmSize,
	}
}

// InvalidateCache clears the cached content, forcing a reload on next Load().
func (wm *WorkingMemory) InvalidateCache() {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	wm.overviewContent = ""
	wm.overviewModTime = time.Time{}
}

// MarkUpdated marks that the working memory has been updated by the user.
// This resets the roundsSinceUpdate counter.
// MarkUpdated marks that working memory has been updated.
// This resets the roundsSinceUpdate counter.
func (wm *WorkingMemory) MarkUpdated() {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	wm.lastUpdateTime = time.Now()
	wm.roundsSinceUpdate = 0
}

// IncrementRound increments the round counter.
// This should be called from the agent loop on each LLM request.
// Call MarkUpdated() when the LLM actually updates working memory.
func (wm *WorkingMemory) IncrementRound() {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	wm.roundsSinceUpdate++
}

// GetRoundsSinceUpdate returns the number of rounds since the last update.
func (wm *WorkingMemory) GetRoundsSinceUpdate() int {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return wm.roundsSinceUpdate
}

// checkUpdateNeeded checks if a reminder should be shown about updating working memory.
// Returns (shouldShowReminder, reminderMessage).
// NOTE: This method does NOT auto-increment the round counter.
// Round tracking should be done via IncrementRound() from the agent loop.
func (wm *WorkingMemory) checkUpdateNeeded() (bool, string) {
	wm.mu.Lock()
	rounds := wm.roundsSinceUpdate
	wm.mu.Unlock()

	// Don't check if we haven't tracked any rounds yet
	if rounds <= 0 {
		return false, ""
	}

	// Don't check before minimum rounds
	if rounds < minRoundsBeforeCheck {
		return false, ""
	}

	// Get meta for token-based thresholds
	meta := wm.GetMeta()

	wm.mu.Lock()
	defer wm.mu.Unlock()

	// Check if we need to remind based on rounds
	if wm.roundsSinceUpdate > maxRoundsWithoutUpdate {
		return true, wm.buildReminderHTML(meta)
	}

	// Optional: remind based on token usage
	if meta.TokensPercent > 70 && wm.roundsSinceUpdate > 3 {
		return true, wm.buildReminderHTML(meta)
	}

	return false, ""
}

// buildReminderHTML builds an HTML comment reminder (appended to working memory content).
func (wm *WorkingMemory) buildReminderHTML(meta ContextMeta) string {
	return fmt.Sprintf(`

<!--
⚠️ WORKING MEMORY UPDATE NEEDED

你已经连续 %d 轮没有更新 working memory 了。
当前上下文状态:
- Token 使用: %.0f%% (%d / %d)
- 历史消息: %d 条
- Working Memory 大小: %.2f KB

建议操作:
1. 总结已完成的任务，归档到 %s
2. 更新"当前任务"状态和进度
3. 删除过时信息，保留最近决策
4. 将详细讨论移到 detail/ 目录

使用 write tool 更新: %s
-->`,
		wm.roundsSinceUpdate,
		meta.TokensPercent,
		meta.TokensUsed,
		meta.TokensMax,
		meta.MessagesInHistory,
		float64(meta.WorkingMemorySize)/1024,
		wm.detailPath,
		wm.overviewPath)
}

// NeedsReminderMessage checks if a reminder message should be injected.
// This is a separate check from checkUpdateNeeded() to allow for different thresholds.
func (wm *WorkingMemory) NeedsReminderMessage() bool {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	// Require more rounds before injecting a separate message
	return wm.roundsSinceUpdate >= maxRoundsWithoutUpdate
}

// GetReminderUserMessage builds a user message reminder to inject into the conversation.
// The message is clearly marked as agent-generated, not from a real user.
func (wm *WorkingMemory) GetReminderUserMessage() string {
	meta := wm.GetMeta()

	wm.mu.RLock()
	rounds := wm.roundsSinceUpdate
	wm.mu.RUnlock()

	return fmt.Sprintf(`[system message by agent, not from real user]

💡 Remember to update your working memory to track progress and compress context if needed.

<context_meta>
tokens_used: %d
tokens_max: %d
tokens_percent: %.0f%%
messages_in_history: %d
rounds_since_update: %d
</context_meta>

Working memory path: %s
Detail directory: %s

To update: use the write tool to modify the working memory file.
This reminder will stop appearing once you update your working memory.`, meta.TokensUsed, meta.TokensMax, meta.TokensPercent, meta.MessagesInHistory, rounds, wm.overviewPath, wm.detailPath)
}

// SaveCompactionSummary saves a compaction summary to the detail directory.
// This allows recall_memory to search through past compaction summaries.
func (wm *WorkingMemory) SaveCompactionSummary(summary string) (string, error) {
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

	slog.Info("[WorkingMemory] Saved compaction summary", "path", fullpath)

	// Return relative path from session directory
	return filepath.Join("working-memory", "detail", filename), nil
}

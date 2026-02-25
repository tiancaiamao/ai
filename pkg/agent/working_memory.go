package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	workingMemoryDir = "working-memory"
	overviewFile     = "overview.md"
	detailDir        = "detail"
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
// It provides caching based on file modification time.
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
- 文件路径使用绝对路径
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
func (wm *WorkingMemory) Load() (string, error) {
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
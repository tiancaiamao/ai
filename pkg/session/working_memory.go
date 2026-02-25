package session

import (
	"os"
	"path/filepath"
)

const (
	// WorkingMemoryDir is the directory name for working memory
	WorkingMemoryDir = "working-memory"
	// OverviewFile is the filename for the overview (L1)
	OverviewFile = "overview.md"
	// DetailDir is the directory name for detailed content (L2)
	DetailDir = "detail"
)

// GetOverviewTemplate returns the default template for overview.md
func GetOverviewTemplate() string {
	return `# Working Memory

<!--
这是你的外部记忆。每次请求时，这个文件的内容会被加载到你的 prompt 中。
你自己决定记住什么、丢弃什么。

你的 context 是有限的。你需要自己决定：
- 什么时候压缩
- 记住什么信息
- 丢弃什么历史

使用 write tool 更新此文件：working-memory/overview.md
下次请求时，你会看到自己写的内容。

这是 YOUR memory。你控制你看到的内容。

每次请求会附带上下文元信息：
- tokens_used: 已使用的 token 数
- tokens_max: 最大 token 数
- tokens_percent: 使用百分比
- messages_in_history: 历史消息数量
- working_memory_size: working memory 大小（字节）
-->

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
- 需要保存详细内容时，写入 working-memory/detail/ 目录
- 文件路径相对于 session 目录
-->
`
}

// EnsureWorkingMemory creates the working-memory directory structure if it doesn't exist.
// Returns the path to the overview.md file.
func EnsureWorkingMemory(sessionDir string) (string, error) {
	wmDir := filepath.Join(sessionDir, WorkingMemoryDir)
	if err := os.MkdirAll(wmDir, 0755); err != nil {
		return "", err
	}

	detailDir := filepath.Join(wmDir, DetailDir)
	if err := os.MkdirAll(detailDir, 0755); err != nil {
		return "", err
	}

	overviewPath := filepath.Join(wmDir, OverviewFile)
	if _, err := os.Stat(overviewPath); os.IsNotExist(err) {
		template := GetOverviewTemplate()
		if err := os.WriteFile(overviewPath, []byte(template), 0644); err != nil {
			return "", err
		}
	}

	return overviewPath, nil
}

// GetWorkingMemoryPath returns the path to overview.md for a session.
func GetWorkingMemoryPath(sessionDir string) string {
	return filepath.Join(sessionDir, WorkingMemoryDir, OverviewFile)
}

// GetWorkingMemoryDetailDir returns the path to the detail directory for a session.
func GetWorkingMemoryDetailDir(sessionDir string) string {
	return filepath.Join(sessionDir, WorkingMemoryDir, DetailDir)
}
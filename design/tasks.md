# Context Snapshot Architecture Implementation Tasks

本文档将新架构的实现拆解为详细的任务。每个任务都有足够的上下文，可以独立地分配给 subagent 完成。

## 概述

**实施原则：**
- 完全重写，不需要保持与旧代码的兼容性
- 保留基础设施：traceevent、llm protocol、skills、tools framework、rpc
- 任务之间有明确的依赖关系
- 每个任务完成后可独立验证

---

## Phase 1: Core Data Structures (pkg/context/)

### 目标
定义新架构的核心数据结构：ContextSnapshot、AgentState、AgentMode、JournalEntry 等。

### 依赖
无（最先开始）

---

#### Task 1.1: Create Basic Types and Enums

**文件**: `pkg/context/types.go`

**任务描述**:
创建基础的类型定义，包括 AgentMode 和相关常量。

**代码结构**:
```go
package context

type AgentMode string

const (
    ModeNormal        AgentMode = "normal"
    ModeContextMgmt   AgentMode = "context_management"
)
```

**验收标准**:
- [X] `AgentMode` 类型定义
- [X] 两个模式常量
- [X] 基本的类型验证方法（如果需要）

---

#### Task 1.2: Create AgentState Structure

**文件**: `pkg/context/agent_state.go`

**任务描述**:
创建 AgentState 结构体，包含系统维护的元数据。

**代码结构**:
```go
package context

import "time"

type AgentState struct {
    // Workspace
    WorkspaceRoot     string
    CurrentWorkingDir string

    // Statistics
    TotalTurns        int
    TokensUsed        int
    TokensLimit       int

    // Tracking
    LastLLMContextUpdate int  // Last turn when LLMContext was updated
    LastCheckpoint       int  // Last turn when checkpoint was created
    LastTriggerTurn      int  // Last turn when context management was triggered
    TurnsSinceLastTrigger int // Turns elapsed since last trigger

    // Active tool calls (for pairing protection)
    ActiveToolCalls     []string

    // Metadata
    SessionID      string
    CreatedAt      time.Time
    UpdatedAt      time.Time
}
```

**参考**: 旧代码中的 `pkg/context/context.go` 中的 AgentContext，但简化许多字段。

**验收标准**:
- [X] AgentState 结构体定义
- [X] 所有必需字段
- [X] JSON 序列化支持
- [X] 基本的初始化函数 `NewAgentState(sessionID, cwd string)`

---

#### Task 1.3: Create ContextSnapshot Structure

**文件**: `pkg/context/snapshot.go`

**任务描述**:
创建 ContextSnapshot 结构体，这是内存中的核心状态表示。

**代码结构**:
```go
package context

type ContextSnapshot struct {
    // 1. LLMContext - LLM-maintained structured context
    LLMContext string

    // 2. RecentMessages - Recent conversation history
    RecentMessages []AgentMessage

    // 3. AgentState - System-maintained metadata
    AgentState AgentState
}

// NewContextSnapshot creates a new empty snapshot
func NewContextSnapshot(sessionID, cwd string) *ContextSnapshot

// Clone creates a deep copy of the snapshot
func (s *ContextSnapshot) Clone() *ContextSnapshot
```

**AgentMessage 结构** - 需要更新以支持新的字段：
```go
type AgentMessage struct {
    Role      string         // "user", "assistant", "toolResult"
    Content   []ContentBlock // Message content
    ToolCallID string         // For tool results
    ToolName  string         // For tool results

    // Truncation tracking
    Truncated     bool   `json:"truncated,omitempty"`
    TruncatedAt   int    `json:"truncated_at,omitempty"`
    OriginalSize  int    `json:"original_size,omitempty"`

    // Visibility control
    AgentVisible  bool   `json:"agent_visible"`
    UserVisible   bool   `json:"user_visible"`

    // Timestamp for ordering
    Timestamp int64  `json:"timestamp"`
}
```

**参考**:
- 旧代码: `pkg/context/message.go`
- 旧代码: `pkg/context/context.go` 中的 AgentContext

**验收标准**:
- [X] ContextSnapshot 结构体定义
- [X] NewContextSnapshot 初始化函数
- [X] Clone 方法实现
- [X] AgentMessage 结构体定义（包含新字段）
- [X] 结构体可以通过 json.Marshal/Unmarshal 正确序列化

---

#### Task 1.4: Create Journal Entry Types

**文件**: `pkg/context/journal.go`

**任务描述**:
创建 messages.jsonl 的日志条目类型，支持消息事件和截断事件。

**代码结构**:
```go
package context

import "time"

// JournalEntry represents a line in messages.jsonl
type JournalEntry struct {
    Type     string        `json:"type"` // "message" | "truncate"
    Message  *AgentMessage `json:"message,omitempty"`
    Truncate *TruncateEvent `json:"truncate,omitempty"`
}

// TruncateEvent represents a truncate operation
type TruncateEvent struct {
    ToolCallID string `json:"tool_call_id"`
    Turn       int    `json:"turn"`
    Trigger    string `json:"trigger"` // "context_management" | "manual"
    Timestamp  string `json:"timestamp"`
}

// NewMessageEntry creates a journal entry for a message
func NewMessageEntry(msg AgentMessage) JournalEntry

// NewTruncateEntry creates a journal entry for a truncate operation
func NewTruncateEntry(toolCallID string, turn int, trigger string) JournalEntry
```

**验收标准**:
- [X] JournalEntry 结构体定义
- [X] TruncateEvent 结构体定义
- [X] NewMessageEntry 工厂函数
- [X] NewTruncateEntry 工厂函数
- [X] 正确的 JSON 序列化

---

## Phase 2: Event Log and Persistence (pkg/context/)

### 目标
实现新的持久化层：messages.jsonl（事件日志）和 checkpoint 系统。

### 依赖
- Phase 1 完成（数据结构定义）

---

#### Task 2.1: Create Checkpoint Directory Structure

**文件**: `pkg/context/checkpoint.go`

**任务描述**:
实现 checkpoint 目录的创建和管理，包括 `current/` 符号链接。

**代码结构**:
```go
package context

import (
    "path/filepath"
    "os"
    "runtime"
    "fmt"
)

const (
    CheckpointDirPattern = "checkpoint_%05d"
    CurrentLinkName      = "current"
)

// CheckpointInfo represents metadata about a checkpoint
type CheckpointInfo struct {
    Turn               int    `json:"turn"`
    MessageIndex       int    `json:"message_index"`
    Path               string `json:"path"`
    CreatedAt          string `json:"created_at"`
    LLMContextChars    int    `json:"llm_context_chars,omitempty"`
    RecentMessagesCount int   `json:"recent_messages_count,omitempty"`
}

// CreateCheckpointDir creates a new checkpoint directory
func CreateCheckpointDir(sessionDir string, turn int) (*CheckpointInfo, error)

// UpdateCurrentLink updates the current/ symlink to point to the latest checkpoint
func UpdateCurrentLink(sessionDir string, checkpointPath string) error

// LoadLatestCheckpoint loads the most recent checkpoint
func LoadLatestCheckpoint(sessionDir string) (*CheckpointInfo, error)

// LoadCheckpointAtTurn loads a checkpoint at a specific turn
func LoadCheckpointAtTurn(sessionDir string, turn int) (*CheckpointInfo, error)
```

**关键实现细节**:
1. `CreateCheckpointDir`:
   - 创建 `checkpoints/checkpoint_000XX/` 目录
   - XX 是 5 位数字的 turn number（如 00015）
   - 返回 CheckpointInfo

2. `UpdateCurrentLink`:
   - 删除现有的 `current/` 链接
   - 创建新的符号链接（Unix）或 junction（Windows）
   - 使用 `os.Symlink()`，Windows 10+ 支持符号链接

3. `LoadLatestCheckpoint`:
   - 读取 `checkpoint_index.json`
   - 返回最新的 checkpoint

**参考**: 旧代码中的 session 目录结构，但新设计有 checkpoint 子目录。

**验收标准**:
- [X] checkpoint 目录正确创建
- [X] current/ 符号链接正确更新
- [X] 在 macOS/Linux 上符号链接工作
- [X] 错误处理完善

---

#### Task 2.2: Implement Checkpoint Index Management

**文件**: `pkg/context/checkpoint_index.go`

**任务描述**:
实现 checkpoint_index.json 的读写，维护所有 checkpoint 的清单。

**代码结构**:
```go
package context

import (
    "encoding/json"
    "os"
    "path/filepath"
    "time"
)

// CheckpointIndex maintains the list of all checkpoints
type CheckpointIndex struct {
    LatestCheckpointTurn  int             `json:"latest_checkpoint_turn"`
    LatestCheckpointPath  string          `json:"latest_checkpoint_path"`
    Checkpoints          []CheckpointInfo `json:"checkpoints"`
}

// LoadCheckpointIndex loads the checkpoint index from disk
func LoadCheckpointIndex(sessionDir string) (*CheckpointIndex, error)

// SaveCheckpointIndex saves the checkpoint index to disk
func (idx *CheckpointIndex) Save(sessionDir string) error

// AddCheckpoint adds a new checkpoint to the index
func (idx *CheckpointIndex) AddCheckpoint(info CheckpointInfo)

// GetCheckpointAtTurn retrieves checkpoint info for a specific turn
func (idx *CheckpointIndex) GetCheckpointAtTurn(turn int) (*CheckpointInfo, error)
```

**文件格式**:
```json
{
  "latest_checkpoint_turn": 45,
  "latest_checkpoint_path": "checkpoints/checkpoint_00045/",
  "checkpoints": [
    {
      "turn": 15,
      "message_index": 50,
      "path": "checkpoints/checkpoint_00015/",
      "created_at": "2024-03-31T10:00:00Z",
      "llm_context_chars": 850,
      "recent_messages_count": 8
    }
  ]
}
```

**验收标准**:
- [X] CheckpointIndex 结构体定义
- [X] LoadCheckpointIndex 正确解析 JSON
- [X] SaveCheckpointIndex 原子写入（使用临时文件 + rename）
- [X] AddCheckpoint 更新 latest 指针
- [X] 错误处理：文件不存在时返回空索引

---

#### Task 2.3: Implement Checkpoint Save and Load

**文件**: `pkg/context/checkpoint_io.go`

**任务描述**:
实现将 ContextSnapshot 保存到 checkpoint 和从 checkpoint 加载的功能。

**代码结构**:
```go
package context

import (
    "encoding/json"
    "os"
    "path/filepath"
    "time"
)

// SaveCheckpoint saves a ContextSnapshot to a checkpoint directory
func SaveCheckpoint(sessionDir string, snapshot *ContextSnapshot, turn int, messageIndex int) (*CheckpointInfo, error) {
    // 1. Create checkpoint directory
    // 2. Save llm_context.txt
    // 3. Save agent_state.json
    // 4. Update checkpoint_index.json
    // 5. Update current/ symlink
    // 6. Return CheckpointInfo
}

// LoadCheckpoint loads a ContextSnapshot from a checkpoint directory
func LoadCheckpoint(sessionDir string, checkpointInfo *CheckpointInfo) (*ContextSnapshot, error) {
    // 1. Load agent_state.json
    // 2. Load llm_context.txt
    // 3. RecentMessages will be loaded from journal separately
    // 4. Return ContextSnapshot with empty RecentMessages
}

// LoadCheckpointLLMContext loads only the LLM context from a checkpoint
func LoadCheckpointLLMContext(checkpointPath string) (string, error)

// LoadCheckpointAgentState loads only the agent state from a checkpoint
func LoadCheckpointAgentState(checkpointPath string) (*AgentState, error)
```

**关键实现细节**:
1. `llm_context.txt`: 纯文本文件，直接写入 LLMContext 字符串
2. `agent_state.json`: JSON 格式的 AgentState
3. 原子写入：先写临时文件，再 rename

**参考**:
- 旧代码: `pkg/session/entries.go` 中的持久化逻辑
- 但新格式更简单：只有两个文件

**验收标准**:
- [X] SaveCheckpoint 正确保存所有数据
- [X] LoadCheckpoint 正确恢复数据
- [X] 文件格式符合设计文档
- [X] 原子写入，避免损坏
- [X] 错误处理完善

---

#### Task 2.4: Implement Journal (messages.jsonl) I/O

**文件**: `pkg/context/journal.go` (扩展)

**任务描述**:
实现 messages.jsonl 的追加写入和读取，支持消息和截断事件。

**代码结构**:
```go
package context

import (
    "bufio"
    "encoding/json"
    "os"
    "path/filepath"
    "sync"
)

// Journal handles append-only writes to messages.jsonl
type Journal struct {
    filePath string
    mu       sync.Mutex
}

// OpenJournal opens (or creates) the journal file
func OpenJournal(sessionDir string) (*Journal, error)

// AppendMessage appends a message to the journal
func (j *Journal) AppendMessage(msg AgentMessage) error

// AppendTruncate appends a truncate event to the journal
func (j *Journal) AppendTruncate(event TruncateEvent) error

// ReadAll reads all entries from the journal
func (j *Journal) ReadAll() ([]JournalEntry, error)

// ReadFromIndex reads entries starting from a specific message index
func (j *Journal) ReadFromIndex(messageIndex int) ([]JournalEntry, error)

// Close closes the journal file
func (j *Journal) Close() error
```

**关键实现细节**:
1. 文件锁：使用 `sync.Mutex` 保护并发写入
2. 追加模式：打开文件时使用 `os.O_APPEND|os.O_CREATE|os.O_WRONLY`
3. JSON Lines: 每行一个 JSON 对象
4. ReadFromIndex: 跳过前 N 行，用于增量加载

**参考**: 旧代码 `pkg/session/entries.go`，但新格式支持 truncate 事件。

**验收标准**:
- [X] Journal 结构体和基本方法
- [X] AppendMessage 正确写入 JSON
- [X] AppendTruncate 正确写入 JSON
- [X] ReadAll 正确解析所有条目
- [X] ReadFromIndex 正确跳过前 N 行
- [X] 并发安全

---

#### Task 2.5: Implement Snapshot Reconstruction from Journal

**文件**: `pkg/context/reconstruction.go`

**任务描述**:
实现从 checkpoint + journal 重建 ContextSnapshot 的逻辑。

**代码结构**:
```go
package context

import (
    "fmt"
)

// ReconstructSnapshot builds a ContextSnapshot from a checkpoint and journal entries
func ReconstructSnapshot(checkpoint *CheckpointInfo, journalEntries []JournalEntry) (*ContextSnapshot, error) {
    // 1. Load checkpoint data (LLMContext, AgentState)
    // 2. Replay journal entries starting from checkpoint.MessageIndex
    // 3. For each entry:
    //    - type="message": append to RecentMessages
    //    - type="truncate": mark message as truncated
    // 4. Return reconstructed snapshot
}

// ApplyTruncateToSnapshot marks a message as truncated in the snapshot
func ApplyTruncateToSnapshot(snapshot *ContextSnapshot, toolCallID string) error {
    // Find message by ToolCallID and set Truncated=true
}

// IsTruncated checks if a message is truncated
func (m AgentMessage) IsTruncated() bool {
    if m.Truncated {
        return true
    }
    return false
}

// IsAgentVisible checks if a message should be visible to the agent
func (m AgentMessage) IsAgentVisible() bool {
    return m.AgentVisible
}
```

**关键实现细节**:
1. 从 checkpoint 加载基础状态
2. 按顺序重放 journal 事件
3. truncate 事件不删除消息，只设置标志
4. 最终的 RecentMessages 包含所有消息，部分被标记为 truncated

**验收标准**:
- [X] ReconstructSnapshot 正确重放事件
- [X] truncate 事件正确标记消息
- [X] 最终的 snapshot 状态正确
- [X] 错误处理：找不到消息时返回错误

---

## Phase 3: Trigger System (pkg/context/)

### 目标
实现触发条件检查系统，决定何时进入 Context Management 模式。

### 依赖
- Phase 1 完成（数据结构）

---

#### Task 3.1: Create Trigger Configuration

**文件**: `pkg/context/trigger_config.go`

**任务描述**:
定义触发条件的常量配置。

**代码结构**:
```go
package context

const (
    // Trigger conditions
    IntervalTurns      = 10  // Check every 10 turns
    MinTurns           = 5   // Don't trigger before turn 5
    TokenThreshold     = 0.40 // 40% token usage
    TokenUrgent        = 0.75 // 75% urgent mode
    StaleCount         = 15  // 15 stale outputs
    MinInterval        = 3   // Min 3 turns between normal triggers

    // Message management
    RecentMessagesKeep       = 30  // Protected region size
    RecentMessagesShowInMgmt  = 10  // Messages shown in LLM context during mgmt

    // Tool output formatting
    ToolOutputMaxChars     = 2000
    ToolOutputPreviewHead  = 1800
    ToolOutputPreviewTail  = 200
)
```

**验收标准**:
- [X] 所有常量定义
- [X] 常量值与设计文档一致

---

#### Task 3.2: Implement Token Estimation

**文件**: `pkg/context/token_estimation.go`

**任务描述**:
实现 token 估算逻辑，参考旧代码但适配新结构。

**代码结构**:
```go
package context

const (
    ApproxBytesPerToken = 4  // Conservative: 1 token ≈ 4 bytes/chars
)

// EstimateTokens estimates the token count for a ContextSnapshot
func (s *ContextSnapshot) EstimateTokens() int {
    // 1. If we have actual LLM usage data, use it
    // 2. Otherwise, estimate from snapshot:
    //    - LLMContext: len() / 4
    //    - RecentMessages: sum of message sizes / 4
    //    - AgentState: fixed overhead ~200 tokens
}

// EstimateMessageTokens estimates token count for messages
func EstimateMessageTokens(messages []AgentMessage) int {
    total := 0
    for _, msg := range messages {
        if !msg.IsAgentVisible() || msg.IsTruncated() {
            continue
        }
        // Rough estimate: 1 token per 4 characters
        total += len(msg.ExtractText()) / 4
    }
    return total
}

// ExtractText extracts the text content from a message
func (m AgentMessage) ExtractText() string {
    // Concatenate all TextContent blocks
}

// EstimateTokenPercent calculates the percentage of token limit used
func (s *ContextSnapshot) EstimateTokenPercent() float64 {
    if s.AgentState.TokensLimit == 0 {
        return 0
    }
    return float64(s.EstimateTokens()) / float64(s.AgentState.TokensLimit)
}
```

**参考**:
- 旧代码: `pkg/truncate/estimate.go`
- 旧代码: `pkg/context/context.go` 中的 token 估算逻辑

**验收标准**:
- [X] EstimateTokens 返回合理的估算值
- [X] EstimateTokenPercent 正确计算百分比
- [X] ExtractText 正确提取文本内容
- [X] 跳过 truncated 和不可见消息

---

#### Task 3.3: Implement Stale Calculation

**文件**: `pkg/context/stale.go`

**任务描述**:
实现工具输出的陈旧度计算。

**代码结构**:
```go
package context

// CalculateStale calculates the stale score for a tool result
// stale = total visible tool results - position in list (0-indexed from oldest)
// Higher stale = older output
func CalculateStale(resultIndex int, totalVisibleToolResults int) int {
    if totalVisibleToolResults == 0 {
        return 0
    }
    return totalVisibleToolResults - resultIndex - 1
}

// CountStaleOutputs counts tool outputs with stale >= threshold
func (s *ContextSnapshot) CountStaleOutputs(threshold int) int {
    // 1. Get all visible, non-truncated tool results
    // 2. Calculate stale for each
    // 3. Count those with stale >= threshold
}

// GetVisibleToolResults returns all visible, non-truncated tool results
func (s *ContextSnapshot) GetVisibleToolResults() []AgentMessage {
    var results []AgentMessage
    for _, msg := range s.RecentMessages {
        if msg.Role == "toolResult" && !msg.IsTruncated() && msg.IsAgentVisible() {
            results = append(results, msg)
        }
    }
    return results
}
```

**验收标准**:
- [X] CalculateStale 正确计算陈旧度
- [X] CountStaleOutputs 正确计数
- [X] GetVisibleToolResults 返回正确的消息子集

---

#### Task 3.4: Implement TriggerChecker

**文件**: `pkg/context/trigger.go`

**任务描述**:
实现触发条件检查逻辑。

**代码结构**:
```go
package context

import "fmt"

// TriggerChecker evaluates trigger conditions
type TriggerChecker struct {
    // Configuration (use constants from trigger_config.go)
    IntervalTurns      int
    MinTurns           int
    TokenThreshold     float64
    TokenUrgent        float64
    StaleCount         int
    MinInterval        int
}

// NewTriggerChecker creates a new TriggerChecker with default config
func NewTriggerChecker() *TriggerChecker

// NewTriggerCheckerWithConfig creates a TriggerChecker with custom config (for testing)
func NewTriggerCheckerWithConfig(config TriggerConfig) *TriggerChecker

// TriggerConfig allows custom trigger configuration
type TriggerConfig struct {
    IntervalTurns      int
    MinTurns           int
    TokenThreshold     float64
    TokenUrgent        float64
    StaleCount         int
    MinInterval        int
}

// ShouldTrigger checks if context management should be triggered
// Returns (shouldTrigger, urgency, reason)
func (t *TriggerChecker) ShouldTrigger(snapshot *ContextSnapshot) (bool, string, string)

// Trigger urgency levels
const (
    UrgencyNone     = ""
    UrgencyUrgent   = "urgent"   // Ignores minInterval
    UrgencyNormal   = "normal"   // Standard trigger
    UrgencyPeriodic = "periodic" // Routine check
    UrgencySkip     = "skip"     // Context is healthy, skip
)
```

**ShouldTrigger 逻辑**:
```go
func (t *TriggerChecker) ShouldTrigger(snapshot *ContextSnapshot) (bool, string, string) {
    state := snapshot.AgentState

    // 1. Check minimum turn requirement
    if state.TotalTurns < t.MinTurns {
        return false, "", "below_min_turns"
    }

    tokenPercent := snapshot.EstimateTokenPercent()
    staleCount := snapshot.CountStaleOutputs(10) // Count outputs with stale >= 10

    // 2. URGENT mode: token usage critical - ignore minInterval
    if tokenPercent >= t.TokenUrgent {
        return true, UrgencyUrgent, fmt.Sprintf("token_usage_%.0f%%", tokenPercent*100)
    }

    // 3. Check minimum interval for normal triggers
    if state.TurnsSinceLastTrigger < t.MinInterval {
        return false, "", "within_min_interval"
    }

    // 4. Normal trigger conditions
    if tokenPercent >= t.TokenThreshold && staleCount >= 10 {
        return true, UrgencyNormal, "token_and_stale_threshold"
    }

    if tokenPercent >= 0.30 {
        return true, UrgencyNormal, fmt.Sprintf("token_usage_%.0f%%", tokenPercent*100)
    }

    if state.TotalTurns >= 15 && tokenPercent >= 0.25 {
        return true, UrgencyNormal, "periodic_token_check"
    }

    if staleCount >= t.StaleCount {
        return true, UrgencyNormal, fmt.Sprintf("stale_outputs_%d", staleCount)
    }

    // 5. Skip condition: context is healthy
    if state.TotalTurns >= 20 && tokenPercent < 0.30 {
        return false, UrgencySkip, "context_healthy"
    }

    // 6. Periodic check
    if state.TotalTurns % t.IntervalTurns == 0 {
        return true, UrgencyPeriodic, "periodic_check"
    }

    return false, "", "no_trigger"
}
```

**参考**:
- 旧代码: `pkg/context/context.go` 中的 `ShouldShowDecisionReminder`
- 但新逻辑更简单：直接返回是否触发

**验收标准**:
- [X] ShouldTrigger 实现所有触发条件
- [X] Urgent 模式忽略 minInterval
- [X] Skip 条件正确工作
- [X] 返回正确的 urgency 和 reason
- [X] 单元测试覆盖所有触发条件

---

## Phase 4: Context Management Tools (pkg/tools/)

### 目标
实现三个上下文管理工具：update_llm_context, truncate_messages, no_action。

### 依赖
- Phase 1 完成（数据结构）
- Phase 2 完成（持久化）

---

#### Task 4.1: Create Tool Result Rendering

**文件**: `pkg/context/render.go`

**任务描述**:
实现模式特定的工具结果渲染：Normal 模式隐藏 ID，Context Management 模式暴露 ID 和元数据。

**代码结构**:
```go
package context

import (
    "fmt"
    "strings"
)

// RenderToolResult renders a tool result for LLM consumption
// Mode-specific: Normal mode hides ID, ContextMgmt mode exposes metadata
func RenderToolResult(msg AgentMessage, mode AgentMode, stale int) string {
    if mode == ModeNormal {
        // Standard rendering, hide ID
        return msg.RenderContent()
    }

    if mode == ModeContextMgmt {
        // Special rendering, expose ID + metadata
        content := msg.ExtractText()

        // Handle large output preview
        if len(content) > ToolOutputMaxChars {
            head := content[:ToolOutputPreviewHead]
            tail := content[len(content)-ToolOutputPreviewTail:]
            truncatedChars := len(content) - ToolOutputPreviewHead - ToolOutputPreviewTail
            content = fmt.Sprintf("%s\n... (%d chars truncated) ...\n%s",
                head, truncatedChars, tail)
        }

        return fmt.Sprintf(
            `<agent:tool id="%s" name="%s" stale="%d" chars="%d">%s</agent:tool>`,
            msg.ToolCallID, msg.ToolName, stale, len(msg.ExtractText()), content,
        )
    }

    return msg.RenderContent()
}

// RenderContent renders just the content without metadata
func (m AgentMessage) RenderContent() string {
    // Extract and return text content
}
```

**验收标准**:
- [X] Normal 模式隐藏 tool_call_id
- [X] ContextMgmt 模式暴露完整的 `<agent:tool>` 标签
- [X] 大输出预览正确截断
- [X] stale 和 chars 正确显示

---

#### Task 4.2: Implement UpdateLLMContext Tool

**文件**: `pkg/tools/context_mgmt/update_llm_context.go`

**任务描述**:
实现 update_llm_context 工具。

**代码结构**:
```go
package context_mgmt

import (
    "context"
    "fmt"
    "path/filepath"

    "github.com/user/project/ai/pkg/context"
    "github.com/user/project/ai/pkg/traceevent"
)

// UpdateLLMContextTool updates the LLMContext
type UpdateLLMContextTool struct {
    sessionDir string
}

// Name returns the tool name
func (t *UpdateLLMContextTool) Name() string {
    return "update_llm_context"
}

// Description returns the tool description
func (t *UpdateLLMContextTool) Description() string {
    return "Update the LLM-maintained structured context. Use this to record progress, decisions, and important information."
}

// Parameters returns the JSON schema for parameters
func (t *UpdateLLMContextTool) Parameters() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "llm_context": map[string]any{
                "type":        "string",
                "description": "The new LLM context in markdown format",
            },
        },
        "required": []string{"llm_context"},
    }
}

// Execute updates the LLM context
func (t *UpdateLLMContextTool) Execute(ctx context.Context, params map[string]any) ([]context.ContentBlock, error) {
    llmContext, ok := params["llm_context"].(string)
    if !ok || llmContext == "" {
        return nil, fmt.Errorf("llm_context is required and must be non-empty")
    }

    // Save to current checkpoint directory
    currentCheckpointPath := filepath.Join(t.sessionDir, "current")
    llmContextPath := filepath.Join(currentCheckpointPath, "llm_context.txt")

    if err := os.WriteFile(llmContextPath, []byte(llmContext), 0644); err != nil {
        return nil, fmt.Errorf("failed to write llm_context.txt: %w", err)
    }

    traceevent.Log(ctx, "context_mgmt_llm_context_updated",
        traceevent.Field{Key: "chars", Value: len(llmContext)},
        traceevent.Field{Key: "checkpoint_path", Value: currentCheckpointPath},
    )

    return []context.ContentBlock{
        context.TextContent{Type: "text", Text: "LLM Context updated."},
    }, nil
}
```

**验收标准**:
- [X] 工具实现符合 Tool 接口
- [X] 正确写入 llm_context.txt
- [X] emit traceevent
- [X] 错误处理完善

---

#### Task 4.3: Implement TruncateMessages Tool

**文件**: `pkg/tools/context_mgmt/truncate_messages.go`

**任务描述**:
实现 truncate_messages 工具。

**代码结构**:
```go
package context_mgmt

import (
    "context"
    "fmt"
    "strings"

    "github.com/user/project/ai/pkg/context"
    "github.com/user/project/ai/pkg/traceevent"
)

// TruncateMessagesTool truncates old tool outputs
type TruncateMessagesTool struct {
    snapshot *context.ContextSnapshot
    journal  *context.Journal
}

// Name returns the tool name
func (t *TruncateMessagesTool) Name() string {
    return "truncate_messages"
}

// Description returns the tool description
func (t *TruncateMessagesTool) Description() string {
    return "Truncate old tool outputs to save context space. Specify message IDs to truncate."
}

// Parameters returns the JSON schema for parameters
func (t *TruncateMessagesTool) Parameters() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "message_ids": map[string]any{
                "type":        "string",
                "description": "Comma-separated tool call IDs to truncate",
            },
        },
        "required": []string{"message_ids"},
    }
}

// Execute truncates the specified messages
func (t *TruncateMessagesTool) Execute(ctx context.Context, params map[string]any) ([]context.ContentBlock, error) {
    idsRaw, ok := params["message_ids"].(string)
    if !ok || idsRaw == "" {
        return nil, fmt.Errorf("message_ids is required")
    }

    // Parse and validate IDs
    ids := strings.Split(idsRaw, ",")
    var validIDs []string
    for _, id := range ids {
        id = strings.TrimSpace(id)
        if id == "" {
            continue
        }
        if !t.isValidToolCallID(id) {
            traceevent.Log(ctx, "context_mgmt_invalid_id",
                traceevent.Field{Key: "id", Value: id},
            )
            continue
        }
        validIDs = append(validIDs, id)
    }

    if len(validIDs) == 0 {
        return nil, fmt.Errorf("no valid tool call IDs provided")
    }

    // Apply truncate
    count := t.applyTruncate(ctx, validIDs)

    traceevent.Log(ctx, "context_mgmt_messages_truncated",
        traceevent.Field{Key: "count", Value: count},
        traceevent.Field{Key: "ids", Value: strings.Join(validIDs, ",")})

    return []context.ContentBlock{
        context.TextContent{Type: "text", Text: fmt.Sprintf("Truncated %d messages.", count)},
    }, nil
}

// isValidToolCallID checks if the ID is a valid tool call ID
func (t *TruncateMessagesTool) isValidToolCallID(id string) bool {
    // Check if there's a message with this ToolCallID
    for _, msg := range t.snapshot.RecentMessages {
        if msg.ToolCallID == id {
            return true
        }
    }
    return false
}

// applyTruncate marks messages as truncated and records to journal
func (t *TruncateMessagesTool) applyTruncate(ctx context.Context, ids []string) int {
    count := 0
    for _, id := range ids {
        // Mark as truncated in snapshot
        for i := range t.snapshot.RecentMessages {
            if t.snapshot.RecentMessages[i].ToolCallID == id {
                t.snapshot.RecentMessages[i].Truncated = true
                t.snapshot.RecentMessages[i].TruncatedAt = t.snapshot.AgentState.TotalTurns
                t.snapshot.RecentMessages[i].OriginalSize = len(t.snapshot.RecentMessages[i].ExtractText())

                // Record to journal
                t.journal.AppendTruncate(context.TruncateEvent{
                    ToolCallID: id,
                    Turn:       t.snapshot.AgentState.TotalTurns,
                    Trigger:    "context_management",
                    Timestamp:  time.Now().Format(time.RFC3339),
                })

                count++
                break
            }
        }
    }
    return count
}
```

**验收标准**:
- [ ] 工具实现符合 Tool 接口
- [ ] 正确解析逗号分隔的 ID 列表
- [ ] 验证 ID 有效性
- [X] 标记消息为 truncated
- [X] 写入 truncate 事件到 journal
- [X] emit traceevent
- [X] 错误处理完善

---

#### Task 4.4: Implement NoAction Tool

**文件**: `pkg/tools/context_mgmt/no_action.go`

**任务描述**:
实现 no_action 工具。

**代码结构**:
```go
package context_mgmt

import (
    "context"
    "fmt"

    "github.com/user/project/ai/pkg/context"
    "github.com/user/project/ai/pkg/traceevent"
)

// NoActionTool indicates no context management is needed
type NoActionTool struct {
    snapshot *context.ContextSnapshot
}

// Name returns the tool name
func (t *NoActionTool) Name() string {
    return "no_action"
}

// Description returns the tool description
func (t *NoActionTool) Description() string {
    return "Indicate that no context management is needed this cycle. Context is healthy."
}

// Parameters returns the JSON schema for parameters
func (t *NoActionTool) Parameters() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{},
    }
}

// Execute handles the no_action case
func (t *NoActionTool) Execute(ctx context.Context, params map[string]any) ([]context.ContentBlock, error) {
    // Update LastTriggerTurn to enforce minInterval before next trigger
    t.snapshot.AgentState.LastTriggerTurn = t.snapshot.AgentState.TotalTurns
    t.snapshot.AgentState.TurnsSinceLastTrigger = 0

    traceevent.Log(ctx, "context_mgmt_no_action",
        traceevent.Field{Key: "turn", Value: t.snapshot.AgentState.TotalTurns})

    return []context.ContentBlock{
        context.TextContent{Type: "text", Text: "No action taken. Context is healthy."},
    }, nil
}
```

**验收标准**:
- [X] 工具实现符合 Tool 接口
- [X] 更新 LastTriggerTurn
- [X] emit traceevent
- [X] 不创建 checkpoint（调用方决定）

---

#### Task 4.5: Create Context Management Tool Registry

**文件**: `pkg/tools/context_mgmt/registry.go`

**任务描述**:
创建上下文管理模式的工具注册。

**代码结构**:
```go
package context_mgmt

import (
    "github.com/user/project/ai/pkg/context"
)

// GetContextMgmtTools returns the tools available in Context Management mode
func GetContextMgmtTools(snapshot *context.ContextSnapshot, journal *context.Journal) []context.Tool {
    return []context.Tool{
        &UpdateLLMContextTool{sessionDir: /* from session */},
        &TruncateMessagesTool{snapshot: snapshot, journal: journal},
        &NoActionTool{snapshot: snapshot},
    }
}
```

**验收标准**:
- [X] 返回三个上下文管理工具
- [X] 工具正确初始化（传入必要的依赖）

---

## Phase 5: LLM Request Building (pkg/llm/ or pkg/prompt/)

### 目标
实现从 ContextSnapshot 构建 LLM 请求的逻辑，包括模式特定的渲染。

### 依赖
- Phase 1 完成（数据结构）
- Phase 4 完成（工具渲染）

---

#### Task 5.1: Implement Mode-Specific Prompt Builder

**文件**: `pkg/prompt/builder_new.go`

**任务描述**:
实现模式特定的 system prompt 构建。

**代码结构**:
```go
package prompt

import (
    "github.com/user/project/ai/pkg/context"
)

// BuildSystemPrompt builds the system prompt for the given mode
func BuildSystemPrompt(mode context.AgentMode) string {
    switch mode {
    case context.ModeNormal:
        return NormalSystemPrompt
    case context.ModeContextMgmt:
        return ContextMgmtSystemPrompt
    default:
        return NormalSystemPrompt
    }
}

const (
    // NormalSystemPrompt is the system prompt for normal mode
    NormalSystemPrompt = `You are a helpful coding assistant. Help users with their programming tasks.

<capabilities>
- Read and write files
- Run commands
- Search code
- Analyze problems
- Debug issues
</capabilities>

<guidelines>
- Respond concisely
- Focus on the task at hand
- Show reasoning when helpful
- Ask for clarification when needed
</guidelines>

The system will automatically manage context size. You don't need to worry about token limits.
`

    // ContextMgmtSystemPrompt is the system prompt for context management mode
    ContextMgmtSystemPrompt = `<system mode="context_management">

You are in CONTEXT MANAGEMENT MODE. Your task is to review and reshape the conversation context.

⚠️ IMPORTANT: This is NOT a normal conversational turn. Do NOT respond to any user message.

<instructions>
Review the provided context and decide what action to take.

AVAILABLE ACTIONS:
1. **update_llm_context** - Rewrite the LLM Context to reflect current state
2. **truncate_messages** - Remove old tool outputs to save space
3. **no_action** - Context is healthy, no action needed

DECISION GUIDELINES:

**When to use update_llm_context:**
- Task has progressed or changed
- New files have been introduced
- Decisions have been made
- Errors were encountered or resolved
- Completed steps should be recorded

**When to use truncate_messages:**
- Old exploration outputs (grep, find) are no longer needed
- Large file reads that are no longer relevant
- Completed task results that won't be referenced again
- Duplicate or redundant outputs

**When to use no_action:**
- Context is healthy (tokens < 30%)
- No stale outputs to remove
- Recently created checkpoint

**TRUNCATION PRIORITIES:**
1. Exploration outputs (grep, find)
2. Large file reads (>2000 chars)
3. Completed task results
4. Preserve: current task data, recent decisions, active work

**STALE SCORE REFERENCE:**
- Higher stale value = older output
- stale >= 10: Consider truncation
- stale >= 20: High priority for truncation

If you choose update_llm_context, provide a new LLM Context following this template:

## Current Task
<one sentence description>
Status: <in_progress|completed|blocked>

## Completed Steps
<bullet list of completed items, each on one line>

## Next Steps
<bullet list of next actions, each on one line>

## Key Files
- <filename>: <brief description>
- <filename>: <brief description>

## Recent Decisions
- <decision made> (reason: <why it was made>)
- <decision made> (reason: <why it was made>)

## Open Issues
- <issue description> (status: <open|resolved|in_progress>)

Keep the LLM Context concise but complete. Aim for 500-1000 tokens.

</instructions>

</system>
`
)
```

**验收标准**:
- [ ] BuildSystemPrompt 正确返回模式特定的 prompt
- [ ] NormalSystemPrompt 简洁、聚焦任务
- [ ] ContextMgmtSystemPrompt 包含完整的指导

---

#### Task 5.2: Implement LLM Request Builder

**文件**: `pkg/llm/request_builder.go`

**任务描述**:
实现从 ContextSnapshot 构建 LLM 请求的逻辑。

**代码结构**:
```go
package llm

import (
    "fmt"

    "github.com/user/project/ai/pkg/context"
    "github.com/user/project/ai/pkg/prompt"
)

// BuildRequest builds an LLM request from a ContextSnapshot
func BuildRequest(snapshot *context.ContextSnapshot, mode context.AgentMode, tools []context.Tool) (*LLMRequest, error) {
    request := &LLMRequest{
        Model: /* from config */,
    }

    // 1. System prompt (stable for caching)
    request.SystemPrompt = prompt.BuildSystemPrompt(mode)

    // 2. RecentMessages (only non-truncated, agent-visible)
    toolResults := snapshot.GetVisibleToolResults()

    for _, msg := range snapshot.RecentMessages {
        if !msg.IsAgentVisible() {
            continue
        }
        if msg.IsTruncated() {
            continue
        }

        // Mode-specific rendering for tool results
        var content string
        if mode == context.ModeContextMgmt && msg.Role == "toolResult" {
            // Calculate stale for this tool result
            stale := calculateStaleForMessage(msg, toolResults)
            content = context.RenderToolResult(msg, mode, stale)
        } else {
            content = msg.RenderContent()
        }

        request.Messages = append(request.Messages, LLMMessage{
            Role:    msg.Role,
            Content: content,
        })
    }

    // 3. Inject <agent:xxx> messages BEFORE last user message
    lastUserIndex := findLastUserMessageIndex(request.Messages)

    // Inject llm_context (if exists and in normal mode)
    if mode == context.ModeNormal && snapshot.LLMContext != "" {
        llmContextMsg := LLMMessage{
            Role:    "user",
            Content: fmt.Sprintf("<agent:llm_context>\n%s\n</agent:llm_context>", snapshot.LLMContext),
        }
        request.Messages = insertBefore(request.Messages, lastUserIndex, llmContextMsg)
    }

    // Inject runtime_state (in both modes for visibility)
    runtimeStateMsg := LLMMessage{
        Role:    "user",
        Content: buildRuntimeStateXML(snapshot, mode),
    }
    request.Messages = insertBefore(request.Messages, lastUserIndex, runtimeStateMsg)

    // 4. Tools (mode-specific)
    request.Tools = ConvertToolsToLLM(tools)

    return request, nil
}

// calculateStaleForMessage calculates the stale score for a specific message
func calculateStaleForMessage(msg context.AgentMessage, allToolResults []context.AgentMessage) int {
    for i, result := range allToolResults {
        if result.ToolCallID == msg.ToolCallID {
            return context.CalculateStale(i, len(allToolResults))
        }
    }
    return 0
}

// findLastUserMessageIndex finds the index of the last user message
func findLastUserMessageIndex(messages []LLMMessage) int {
    for i := len(messages) - 1; i >= 0; i-- {
        if messages[i].Role == "user" {
            return i
        }
    }
    return len(messages) // No user message found, append to end
}

// insertBefore inserts a message at the specified index
func insertBefore(messages []LLMMessage, index int, newMsg LLMMessage) []LLMMessage {
    result := make([]LLMMessage, 0, len(messages)+1)
    result = append(result, messages[:index]...)
    result = append(result, newMsg)
    result = append(result, messages[index:]...)
    return result
}

// buildRuntimeStateXML builds the runtime state XML
func buildRuntimeStateXML(snapshot *context.ContextSnapshot, mode context.AgentMode) string {
    tokenPercent := snapshot.EstimateTokenPercent()
    staleCount := snapshot.CountStaleOutputs(10)

    content := fmt.Sprintf(`<agent:runtime_state>
tokens_used: %d
tokens_limit: %d
tokens_percent: %.1f
recent_messages: %d
stale_outputs: %d
turn: %d
urgency: %s
</agent:runtime_state>`,
        snapshot.EstimateTokens(),
        snapshot.AgentState.TokensLimit,
        tokenPercent*100,
        len(snapshot.RecentMessages),
        staleCount,
        snapshot.AgentState.TotalTurns,
        determineUrgency(snapshot, mode),
    )

    return content
}

// determineUrgency determines the urgency level
func determineUrgency(snapshot *context.ContextSnapshot, mode context.AgentMode) string {
    if mode == context.ModeContextMgmt {
        tokenPercent := snapshot.EstimateTokenPercent()
        if tokenPercent >= 0.75 {
            return "urgent"
        } else if tokenPercent >= 0.40 {
            return "high"
        } else if tokenPercent >= 0.25 {
            return "medium"
        }
    }
    return "none"
}

// LLMRequest represents an LLM API request
type LLMRequest struct {
    Model       string
    SystemPrompt string
    Messages    []LLMMessage
    Tools       []Tool
}
```

**参考**:
- 旧代码: `pkg/prompt/builder.go` 中的 `insertBeforeLastUserMessage`
- 旧代码: `pkg/agent/loop.go` 中的请求构建逻辑

**验收标准**:
- [ ] BuildRequest 正确构建 LLM 请求
- [ ] System prompt 根据模式变化
- [ ] 工具结果在 ContextMgmt 模式下正确渲染
- [ ] llm_context 在 Normal 模式下注入
- [ ] runtime_state 在两种模式下都注入
- [ ] 消息插入到最后一个用户消息之前

---

#### Task 5.3: Build Context Management Input

**文件**: `pkg/llm/context_mgmt_input.go`

**任务描述**:
实现为 Context Management 模式构建专门的输入。

**代码结构**:
```go
package llm

import (
    "fmt"
    "strings"

    "github.com/user/project/ai/pkg/context"
)

// BuildContextMgmtInput builds the specialized input for Context Management mode
func BuildContextMgmtInput(snapshot *context.ContextSnapshot) string {
    var input strings.Builder

    // 1. Current state
    tokenPercent := snapshot.EstimateTokenPercent()
    staleCount := snapshot.CountStaleOutputs(10)

    input.WriteString("<current_state>\n")
    input.WriteString(fmt.Sprintf("Recent messages: %d\n", len(snapshot.RecentMessages)))
    input.WriteString(fmt.Sprintf("Tokens used: %.1f%%\n", tokenPercent*100))
    input.WriteString(fmt.Sprintf("Stale outputs: %d\n", staleCount))
    input.WriteString(fmt.Sprintf("Turns since last management: %d\n",
        snapshot.AgentState.TurnsSinceLastTrigger))
    input.WriteString(fmt.Sprintf("Urgency: %s\n", determineUrgency(snapshot, context.ModeContextMgmt)))
    input.WriteString("</current_state>\n\n")

    // 2. Current LLMContext
    if snapshot.LLMContext != "" {
        input.WriteString("## Current LLM Context\n")
        input.WriteString(snapshot.LLMContext)
        input.WriteString("\n\n")
    }

    // 3. Stale tool outputs (all visible tool results, ordered by stale)
    input.WriteString("## Stale Tool Outputs (candidates for truncation)\n")
    staleOutputs := getStaleToolOutputs(snapshot)
    for _, output := range staleOutputs {
        toolResults := snapshot.GetVisibleToolResults()
        stale := calculateStaleForMessage(output, toolResults)
        input.WriteString(context.RenderToolResult(output, context.ModeContextMgmt, stale))
        input.WriteString("\n")
    }
    input.WriteString("\n")

    // 4. Recent messages (last N)
    input.WriteString(fmt.Sprintf("## Recent Messages (last %d)\n", context.RecentMessagesShowInMgmt))
    recent := getLastNMessages(snapshot.RecentMessages, context.RecentMessagesShowInMgmt)
    for _, msg := range recent {
        if !msg.IsAgentVisible() || msg.IsTruncated() {
            continue
        }
        input.WriteString(msg.RenderContent())
        input.WriteString("\n")
    }

    return input.String()
}

// getStaleToolOutputs returns tool results ordered by staleness
func getStaleToolOutputs(snapshot *context.ContextSnapshot) []context.AgentMessage {
    toolResults := snapshot.GetVisibleToolResults()
    // Already ordered from oldest (highest stale) to newest
    return toolResults
}

// getLastNMessages returns the last N messages
func getLastNMessages(messages []context.AgentMessage, n int) []context.AgentMessage {
    if len(messages) <= n {
        return messages
    }
    return messages[len(messages)-n:]
}
```

**验收标准**:
- [ ] BuildContextMgmtInput 包含所有必需部分
- [ ] 当前状态正确显示
- [ ] Stale 工具输出按陈旧度排序
- [ ] 最近消息数量正确限制

---

## Phase 6: Agent Loop (pkg/agent/)

### 目标
实现新的 Agent Loop，支持两种模式的切换。

### 依赖
- Phase 1-5 全部完成

---

#### Task 6.1: Create New Agent Structure

**文件**: `pkg/agent/agent_new.go`

**任务描述**:
创建新的 Agent 结构体，包含 ContextSnapshot 而不是 AgentContext。

**代码结构**:
```go
package agent

import (
    "context"
    "sync"

    "github.com/user/project/ai/pkg/context"
    "github.com/user/project/ai/pkg/llm"
)

// AgentNew represents the new agent implementation
type AgentNew struct {
    // Core state
    snapshot      *context.ContextSnapshot
    snapshotMu    sync.RWMutex

    // Session
    sessionDir    string
    sessionID     string
    journal       *context.Journal

    // Configuration
    model         *ModelSpec
    triggerChecker *context.TriggerChecker

    // Event emission
    eventEmitter  EventEmitter

    // Tools
    allTools      []context.Tool
}

// NewAgentNew creates a new agent
func NewAgentNew(sessionDir, sessionID string, model *ModelSpec, eventEmitter EventEmitter) (*AgentNew, error) {
    // 1. Load or create snapshot
    // 2. Open journal
    // 3. Initialize trigger checker
    // 4. Load all tools
}

// ModelSpec is the model configuration (reuse from existing code)
// EventEmitter is the event emission interface (reuse from existing code)
```

**验收标准**:
- [ ] AgentNew 结构体定义
- [ ] NewAgentNew 工厂函数
- [ ] 正确初始化所有组件

---

#### Task 6.2: Implement Normal Mode Execution

**文件**: `pkg/agent/loop_normal.go`

**任务描述**:
实现 Normal 模式的执行逻辑：处理用户输入、调用 LLM、执行工具。

**代码结构**:
```go
package agent

import (
    "context"
    "fmt"

    "github.com/user/project/ai/pkg/context"
    "github.com/user/project/ai/pkg/llm"
)

// ExecuteNormalMode executes a turn in normal mode
func (a *AgentNew) ExecuteNormalMode(ctx context.Context, userMessage string) error {
    a.snapshotMu.Lock()
    defer a.snapshotMu.Unlock()

    // 1. Check trigger conditions
    shouldTrigger, urgency, reason := a.triggerChecker.ShouldTrigger(a.snapshot)
    if shouldTrigger && urgency != context.UrgencySkip {
        // Switch to context management mode first
        return a.ExecuteContextMgmtMode(ctx)
    }

    // 2. Append user message to snapshot
    userMsg := context.AgentMessage{
        Role:      "user",
        Content:   []context.ContentBlock{context.TextContent{Type: "text", Text: userMessage}},
        Timestamp: time.Now().Unix(),
        AgentVisible: true,
        UserVisible: true,
    }
    a.snapshot.RecentMessages = append(a.snapshot.RecentMessages, userMsg)

    // 3. Persist to journal
    if err := a.journal.AppendMessage(userMsg); err != nil {
        return fmt.Errorf("failed to append message: %w", err)
    }

    // 4. Build LLM request
    request, err := llm.BuildRequest(a.snapshot, context.ModeNormal, a.allTools)
    if err != nil {
        return fmt.Errorf("failed to build request: %w", err)
    }

    // 5. Call LLM
    stream := llm.StreamLLM(ctx, a.model, request, /* ... */)

    // 6. Process response (similar to existing loop.go)
    return a.processLLMResponse(ctx, stream)
}

// processLLMResponse processes the LLM streaming response
func (a *AgentNew) processLLMResponse(ctx context.Context, stream *llm.EventStream) error {
    // 1. Handle text deltas
    // 2. Handle tool calls
    // 3. Execute tools
    // 4. Append tool results
    // 5. Loop if more tool calls
    // 6. Update turn count
    // 7. Persist to journal
}
```

**参考**:
- 旧代码: `pkg/agent/loop.go` 中的 `runInnerLoop`
- 复用流式响应处理逻辑

**验收标准**:
- [ ] 正确处理用户输入
- [ ] 正确调用 LLM
- [ ] 正确执行工具
- [ ] 正确持久化消息
- [ ] 正确更新 turn count

---

#### Task 6.3: Implement Context Management Mode Execution

**文件**: `pkg/agent/loop_context_mgmt.go`

**任务描述**:
实现 Context Management 模式的执行逻辑。

**代码结构**:
```go
package agent

import (
    "context"
    "fmt"
    "time"

    "github.com/user/project/ai/pkg/context"
    "github.com/user/project/ai/pkg/llm"
    "github.com/user/project/ai/pkg/tools/context_mgmt"
)

// ExecuteContextMgmtMode executes the context management flow
func (a *AgentNew) ExecuteContextMgmtMode(ctx context.Context) error {
    a.snapshotMu.Lock()
    defer a.snapshotMu.Unlock()

    // 1. Build context management input
    input := llm.BuildContextMgmtInput(a.snapshot)

    // 2. Get context management tools
    ctxMgmtTools := context_mgmt.GetContextMgmtTools(a.snapshot, a.journal)

    // 3. Build LLM request for context management
    request := &llm.LLMRequest{
        Model:       a.model,
        SystemPrompt: prompt.BuildSystemPrompt(context.ModeContextMgmt),
        Messages: []llm.LLMMessage{
            {Role: "user", Content: input},
        },
        Tools:       llm.ConvertToolsToLLM(ctxMgmtTools),
    }

    // 4. Call LLM
    stream := llm.StreamLLM(ctx, a.model, request, /* ... */)

    // 5. Process tool calls
    toolCalls, err := a.extractToolCalls(stream)
    if err != nil {
        // Retry logic
        return a.retryContextMgmt(ctx, err)
    }

    // 6. Execute tool calls
    actionTaken := false
    for _, toolCall := range toolCalls {
        if toolCall.Name == "no_action" {
            // Update LastTriggerTurn but don't create checkpoint
            a.executeNoAction(ctx, toolCall)
        } else {
            // Execute the tool
            if err := a.executeContextMgmtTool(ctx, toolCall); err != nil {
                return err
            }
            actionTaken = true
        }
    }

    // 7. Create checkpoint if action was taken
    if actionTaken {
        if err := a.createCheckpoint(ctx); err != nil {
            return fmt.Errorf("failed to create checkpoint: %w", err)
        }
    }

    // 8. Update trigger tracking
    a.snapshot.AgentState.LastTriggerTurn = a.snapshot.AgentState.TotalTurns
    a.snapshot.AgentState.TurnsSinceLastTrigger = 0

    return nil
}

// retryContextMgmt retries context management on failure
func (a *AgentNew) retryContextMgmt(ctx context.Context, originalErr error) error {
    // Retry once with exponential backoff
    // If still fails, return error and resume normal mode
}

// executeNoAction handles the no_action case
func (a *AgentNew) executeNoAction(ctx context.Context, toolCall llm.ToolCall) error {
    // Update snapshot state (already done by NoActionTool)
    // Emit event
    traceevent.Log(ctx, "context_mgmt_no_action", /* ... */)
    return nil
}

// executeContextMgmtTool executes a context management tool
func (a *AgentNew) executeContextMgmtTool(ctx context.Context, toolCall llm.ToolCall) error {
    // Find tool and execute
    // Update snapshot if needed
}

// createCheckpoint creates a new checkpoint
func (a *AgentNew) createCheckpoint(ctx context.Context) error {
    // 1. Determine message index
    messageIndex := a.journal.GetLength() - 1

    // 2. Save checkpoint
    checkpointInfo, err := context.SaveCheckpoint(
        a.sessionDir,
        a.snapshot,
        a.snapshot.AgentState.TotalTurns,
        messageIndex,
    )
    if err != nil {
        return err
    }

    // 3. Update snapshot state
    a.snapshot.AgentState.LastCheckpoint = a.snapshot.AgentState.TotalTurns

    // 4. Emit event
    traceevent.Log(ctx, "checkpoint_created",
        traceevent.Field{Key: "turn", Value: checkpointInfo.Turn},
        traceevent.Field{Key: "checkpoint_path", Value: checkpointInfo.Path},
    )

    return nil
}
```

**验收标准**:
- [ ] 正确构建 context management 输入
- [ ] 正确调用 LLM
- [ ] 正确执行工具
- [ ] no_action 时更新 LastTriggerTurn 但不创建 checkpoint
- [ ] 其他操作时创建 checkpoint
- [ ] 失败时正确重试
- [ ] emit traceevent

---

#### Task 6.4: Implement Session Resume

**文件**: `pkg/agent/resume.go`

**任务描述**:
实现从 checkpoint 和 journal 恢复会话的逻辑。

**代码结构**:
```go
package agent

import (
    "context"
    "fmt"

    "github.com/user/project/ai/pkg/context"
)

// LoadSession loads a session from disk
func LoadSession(ctx context.Context, sessionDir string, model *ModelSpec, eventEmitter EventEmitter) (*AgentNew, error) {
    // 1. Load checkpoint index
    idx, err := context.LoadCheckpointIndex(sessionDir)
    if err != nil {
        // New session
        return createNewSession(sessionDir, model, eventEmitter)
    }

    // 2. Load latest checkpoint
    latestCheckpoint, err := idx.GetCheckpointAtTurn(idx.LatestCheckpointTurn)
    if err != nil {
        return nil, fmt.Errorf("failed to get latest checkpoint: %w", err)
    }

    // 3. Load checkpoint data
    snapshot, err := context.LoadCheckpoint(sessionDir, latestCheckpoint)
    if err != nil {
        return nil, fmt.Errorf("failed to load checkpoint: %w", err)
    }

    // 4. Open journal
    journal, err := context.OpenJournal(sessionDir)
    if err != nil {
        return nil, fmt.Errorf("failed to open journal: %w", err)
    }

    // 5. Read journal entries after checkpoint
    entries, err := journal.ReadFromIndex(latestCheckpoint.MessageIndex + 1)
    if err != nil {
        return nil, fmt.Errorf("failed to read journal: %w", err)
    }

    // 6. Reconstruct snapshot
    snapshot, err = context.ReconstructSnapshot(latestCheckpoint, entries)
    if err != nil {
        return nil, fmt.Errorf("failed to reconstruct snapshot: %w", err)
    }

    // 7. Create agent
    agent := &AgentNew{
        snapshot:      snapshot,
        sessionDir:    sessionDir,
        sessionID:     snapshot.AgentState.SessionID,
        journal:       journal,
        model:         model,
        triggerChecker: context.NewTriggerChecker(),
        eventEmitter:  eventEmitter,
        allTools:      loadAllTools(),
    }

    return agent, nil
}

// createNewSession creates a new session
func createNewSession(sessionDir string, model *ModelSpec, eventEmitter EventEmitter) (*AgentNew, error) {
    sessionID := generateSessionID()

    // Create session directory
    if err := os.MkdirAll(sessionDir, 0755); err != nil {
        return nil, fmt.Errorf("failed to create session dir: %w", err)
    }

    // Create checkpoint directory
    checkpointsDir := filepath.Join(sessionDir, "checkpoints")
    if err := os.MkdirAll(checkpointsDir, 0755); err != nil {
        return nil, fmt.Errorf("failed to create checkpoints dir: %w", err)
    }

    // Create initial snapshot
    snapshot := context.NewContextSnapshot(sessionID, sessionDir)

    // Open journal
    journal, err := context.OpenJournal(sessionDir)
    if err != nil {
        return nil, fmt.Errorf("failed to open journal: %w", err)
    }

    // Create agent
    agent := &AgentNew{
        snapshot:      snapshot,
        sessionDir:    sessionDir,
        sessionID:     sessionID,
        journal:       journal,
        model:         model,
        triggerChecker: context.NewTriggerChecker(),
        eventEmitter:  eventEmitter,
        allTools:      loadAllTools(),
    }

    return agent, nil
}

// loadAllTools loads all available tools (reuse from existing code)
func loadAllTools() []context.Tool {
    // Load all tools from pkg/tools/
}
```

**验收标准**:
- [ ] LoadSession 正确加载现有会话
- [ ] createNewSession 正确创建新会话
- [ ] checkpoint + journal 正确重放
- [ ] 最终 snapshot 状态正确

---

## Phase 7: RPC Integration (cmd/ai/)

### 目标
将新的 Agent 集成到 RPC 层。

### 依赖
- Phase 6 完成（Agent Loop）

---

#### Task 7.1: Update RPC Handlers to Use New Agent

**文件**: `cmd/ai/rpc_handlers_new.go`

**任务描述**:
更新 RPC handlers 以使用新的 Agent 实现。

**代码结构**:
```go
package main

import (
    "github.com/user/project/ai/pkg/agent"
    "github.com/user/project/ai/pkg/rpc"
)

// handleSteerNew handles the steer command with new agent
func (s *Server) handleSteerNew(cmd rpc.PromptRequest) error {
    // 1. Get or create session
    ag, err := agent.LoadSession(s.sessionDir, s.model, s.eventEmitter)
    if err != nil {
        return err
    }

    // 2. Execute in normal mode
    if err := ag.ExecuteNormalMode(context.Background(), cmd.Message); err != nil {
        return err
    }

    return nil
}
```

**注意**:
- RPC 接口保持不变
- 只改变内部实现
- 可以并行存在旧和新实现（通过配置选择）

**验收标准**:
- [ ] handleSteerNew 正确调用新 agent
- [ ] RPC 接口兼容
- [ ] 事件正确发送

---

## Phase 8: Observability (traceevent)

### 目标
为新架构添加全面的 traceevent 日志。

### 依赖
- Phase 1-7 完成

---

#### Task 8.1: Add Context Management Trace Events

**文件**: `pkg/context/events.go`

**任务描述**:
定义和实现上下文管理相关的 trace events。

**代码结构**:
```go
package context

import (
    "github.com/user/project/ai/pkg/traceevent"
)

// Trace event names for context management
const (
    EventContextMgmtTrigger       = "context_mgmt_trigger"
    EventContextMgmtModeSwitch    = "context_mgmt_mode_switch"
    EventContextMgmtToolCall      = "context_mgmt_tool_call"
    EventContextMgmtLLMContextUpdated = "context_mgmt_llm_context_updated"
    EventContextMgmtMessagesTruncated = "context_mgmt_messages_truncated"
    EventContextMgmtNoAction       = "context_mgmt_no_action"
    EventCheckpointCreated         = "checkpoint_created"
    EventCheckpointLoaded          = "checkpoint_loaded"
)

// LogContextMgmtTrigger logs a context management trigger event
func LogContextMgmtTrigger(ctx context.Context, turn int, urgency, reason string, tokenPercent float64, staleCount int) {
    traceevent.Log(ctx, EventContextMgmtTrigger,
        traceevent.Field{Key: "turn", Value: turn},
        traceevent.Field{Key: "urgency", Value: urgency},
        traceevent.Field{Key: "reason", Value: reason},
        traceevent.Field{Key: "tokens_percent", Value: tokenPercent},
        traceevent.Field{Key: "stale_count", Value: staleCount},
    )
}

// LogContextMgmtModeSwitch logs a mode switch event
func LogContextMgmtModeSwitch(ctx context.Context, from, to, reason string) {
    traceevent.Log(ctx, EventContextMgmtModeSwitch,
        traceevent.Field{Key: "from", Value: from},
        traceevent.Field{Key: "to", Value: to},
        traceevent.Field{Key: "reason", Value: reason},
    )
}

// ... other logging functions
```

**验收标准**:
- [ ] 所有 trace event 定义
- [ ] 所有 logging helper 函数
- [ ] 在适当的代码位置调用

---

#### Task 8.2: Register Trace Events in Config

**文件**: `pkg/traceevent/config.go` (修改)

**任务描述**:
将新的 trace events 注册到 traceevent 配置中。

**代码结构**:
```go
// 在 eventNameToBit map 中添加新事件
var eventNameToBit = map[string]uint64{
    // ... existing events ...
    "context_mgmt_trigger":                  1 << 40,
    "context_mgmt_mode_switch":              1 << 41,
    "context_mgmt_tool_call":                1 << 42,
    "context_mgmt_llm_context_updated":      1 << 43,
    "context_mgmt_messages_truncated":       1 << 44,
    "context_mgmt_no_action":                1 << 45,
    "checkpoint_created":                    1 << 46,
    "checkpoint_loaded":                     1 << 47,
}
```

**验收标准**:
- [ ] 新事件正确注册
- [ ] 位分配不冲突

---

## Phase 9: Testing

### 目标
为新架构编写全面的测试。

### 依赖
- Phase 1-8 完成

---

#### Task 9.1: Unit Tests for Trigger System

**文件**: `pkg/context/trigger_test.go`

**任务描述**:
为 TriggerChecker 编写单元测试。

**代码结构**:
```go
package context

import (
    "testing"
)

func TestTriggerChecker_ShouldTrigger_TokenUrgent(t *testing.T) {
    // Test: tokens >= 75% should trigger urgent mode
}

func TestTriggerChecker_ShouldTrigger_TokenThreshold(t *testing.T) {
    // Test: tokens >= 40% with stale >= 10 should trigger
}

func TestTriggerChecker_ShouldTrigger_MinInterval(t *testing.T) {
    // Test: should respect minInterval for non-urgent triggers
}

func TestTriggerChecker_ShouldTrigger_Periodic(t *testing.T) {
    // Test: should trigger every IntervalTurns
}

func TestTriggerChecker_ShouldTrigger_SkipWhenHealthy(t *testing.T) {
    // Test: should skip when tokens < 30% after turn 20
}
```

**验收标准**:
- [ ] 所有触发条件有测试
- [ ] 边界情况覆盖
- [ ] 测试通过

---

#### Task 9.2: Unit Tests for Snapshot Reconstruction

**文件**: `pkg/context/reconstruction_test.go`

**任务描述**:
为快照重建逻辑编写单元测试。

**验收标准**:
- [ ] 测试消息重放
- [ ] 测试 truncate 事件应用
- [ ] 测试边界情况

---

#### Task 9.3: Integration Tests

**文件**: `pkg/agent/integration_test.go`

**任务描述**:
编写端到端的集成测试。

**验收标准**:
- [ ] 测试完整的对话流程
- [ ] 测试上下文管理触发
- [ ] 测试 checkpoint 创建和恢复

---

## Phase 10: Cleanup and Documentation

### 目标
清理旧代码，更新文档。

### 依赖
- Phase 1-9 完成，新架构验证通过

---

#### Task 10.1: Remove Old Context Management Code

**任务描述**:
删除不再需要的旧代码。

**文件列表**:
- `pkg/context/context.go` (旧 AgentContext)
- `pkg/tools/context_management.go` (旧工具)
- `pkg/prompt/context_management.md` (旧 prompt)

**验收标准**:
- [ ] 旧代码删除
- [ ] 编译通过
- [ ] 测试通过

---

#### Task 10.2: Update Documentation

**文件**: `CLAUDE.md`, `README.md`, etc.

**任务描述**:
更新文档以反映新架构。

**验收标准**:
- [ ] 文档更新
- [ ] 示例代码更新

---

## 总结

### 实施顺序建议

1. **Phase 1-2**: 基础数据结构和持久化（1-2 周）
2. **Phase 3**: 触发系统（3-5 天）
3. **Phase 4-5**: 工具和 Prompt 构建（1 周）
4. **Phase 6**: Agent Loop（1-2 周）
5. **Phase 7**: RPC 集成（3-5 天）
6. **Phase 8**: Observability（2-3 天）
7. **Phase 9**: 测试（1 周）
8. **Phase 10**: 清理（2-3 天）

**总计**: 约 6-8 周

### 依赖关系

```
Phase 1 (Data Structures)
    ↓
Phase 2 (Persistence) ←───┐
    ↓                      │
Phase 3 (Trigger) ←───────┘
    ↓
Phase 4 (Tools)
    ↓
Phase 5 (LLM Request) ←───┐
    ↓                      │
Phase 6 (Agent Loop) ←────┘
    ↓
Phase 7 (RPC Integration)
    ↓
Phase 8 (Observability)
    ↓
Phase 9 (Testing)
    ↓
Phase 10 (Cleanup)
```

### 验证检查点

在每个 Phase 完成后，运行相应的验证：
- Phase 1-2: 能够创建和加载 checkpoint
- Phase 3: 触发条件正确工作
- Phase 4-5: 能够构建正确的 LLM 请求
- Phase 6: 能够执行完整的对话流程
- Phase 7: RPC 接口正常工作
- Phase 8: Trace events 正确记录
- Phase 9: 所有测试通过
- Phase 10: 代码清理完成

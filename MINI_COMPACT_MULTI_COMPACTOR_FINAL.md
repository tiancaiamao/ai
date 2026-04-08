# 多 Compactor 并存设计（最终版）

## 核心原则

**接口统一，实现分离，多实例并存**

## 接口设计（已存在）

```go
// pkg/context/compactor.go
type Compactor interface {
    ShouldCompact(messages []AgentMessage) bool
    Compact(messages []AgentMessage, previousSummary string) (*CompactionResult, error)
    CalculateDynamicThreshold() int
    EstimateContextTokens(messages []AgentMessage) int
}
```

## 实现分类

### 1. FullCompact（完整压缩）
- LLM 总结
- 完整的消息压缩
- 对应 `pkg/compact/Compact`

### 2. MiniCompact（轻量截断）
- 仅截断，不总结
- 删除过时的 tool outputs
- 新增实现

## LoopConfig 配置

```go
type LoopConfig struct {
    // 支持多个 Compactor，按数组顺序执行
    Compactors []Compactor

    // 或单独配置（向后兼容）
    FullCompactor  Compactor  // 完整压缩
    MiniCompactor  Compactor  // 轻量截断
}
```

## 执行逻辑（关键）

```go
// runInnerLoop 中

// 方式 1：多 Compactor 并存（推荐）
for _, c := range config.Compactors {
    if c.ShouldCompact(agentCtx.Messages) {
        slog.Info("[Loop] Compaction triggered", "compactor", typeName(c))
        result, err := c.Compact(agentCtx.Messages, agentCtx.LastCompactionSummary)
        // 处理结果...
        break // 第一个触发就停止
    }
}

// 方式 2：单独配置（向后兼容）
if config.MiniCompactor != nil && config.MiniCompactor.ShouldCompact(...) {
    // 执行 mini compact
}
if config.FullCompactor != nil && config.FullCompactor.ShouldCompact(...) {
    // 执行 full compact
}
```

## 优先级控制

通过数组顺序控制优先级：

```go
config.Compactors = []Compactor{
    MiniCompact,  // 先尝试轻量级
    FullCompact,  // 再尝试完整
}
```

## 配置示例

### 只启用 MiniCompact

```json
{
  "compactors": [
    {
      "type": "mini",
      "enabled": true,
      "token_threshold": 30.0
    }
  ]
}
```

### 启用两种（Mini 优先）

```json
{
  "compactors": [
    {
      "type": "mini",
      "enabled": true,
      "token_threshold": 30.0
    },
    {
      "type": "full",
      "enabled": true,
      "auto_compact": true
    }
  ]
}
```

## MiniCompact 实现

```go
type MiniCompact struct {
    config *MiniCompactConfig
    truncater TruncationStrategy
}

func (m *MiniCompact) ShouldCompact(messages []AgentMessage) bool {
    tokens := m.EstimateContextTokens(messages)
    tokensPercent := float64(tokens) / 128000 * 100
    staleCount := m.countStaleOutputs(messages)

    // 触发条件
    return tokensPercent >= 30.0 || staleCount > 5
}

func (m *MiniCompact) Compact(messages []AgentMessage, _ string) (*CompactionResult, error) {
    // 只截断，不总结
    truncated := m.truncateStaleOutputs(messages)
    summary := fmt.Sprintf("Truncated %d stale tool outputs", m.countTruncated(truncated))

    return &CompactionResult{
        Summary:      summary,
        Messages:     truncated,
        TokensBefore: m.EstimateContextTokens(messages),
        TokensAfter:  m.EstimateContextTokens(truncated),
    }, nil
}

func (m *MiniCompact) CalculateDynamicThreshold() int {
    return int(128000 * 0.30) // 30% of context
}

func (m *MiniCompact) EstimateContextTokens(messages []AgentMessage) int {
    // 复用现有的 token 估算逻辑
    // ...
}
```

## 优势

1. ✅ **接口统一** - 都实现 `Compactor`
2. ✅ **实现分离** - `FullCompact` 和 `MiniCompact` 各自独立
3. ✅ **多实例并存** - 通过数组配置
4. ✅ **优先级可控** - 数组顺序决定执行顺序
5. ✅ **易于扩展** - 添加新的 compact 类型
6. ✅ **向后兼容** - 支持 `FullCompactor/MiniCompactor` 单独配置

## 下一步

实现 `MiniCompact`：
1. `pkg/context/mini_compact.go` - MiniCompact 实现
2. 配置加载 - 支持从配置文件加载
3. 集成到 runInnerLoop - 多 Compactor 执行逻辑
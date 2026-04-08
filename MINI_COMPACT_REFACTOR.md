# 复用 Compactor 接口的 Mini Compact 设计

## 现有接口

`pkg/context/compactor.go` 已经定义了：

```go
type Compactor interface {
    ShouldCompact(messages []AgentMessage) bool
    Compact(messages []AgentMessage, previousSummary string) (*CompactionResult, error)
    CalculateDynamicThreshold() int
    EstimateContextTokens(messages []AgentMessage) int
}
```

## 问题

1. `Compactor.Compact` 要求 `previousSummary` 并返回 summary
2. Mini compact 不需要 LLM 总结，只是截断
3. `ShouldCompact` 返回 bool，但需要 urgency/reason 信息

## 解决方案

### 方案 1: 扩展 Compactor 接口（不推荐）

破坏现有接口，向后兼容性差。

### 方案 2: MiniCompact 实现 Compactor（推荐）

创建一个轻量级的 `MiniCompact` 实现：

```go
type MiniCompact struct {
    config *MiniCompactConfig
    truncater TruncationStrategy
}

func (m *MiniCompact) ShouldCompact(messages []AgentMessage) bool {
    // 检查 tokens ≥ 30% 或 stale outputs > 5
    tokens := m.EstimateContextTokens(messages)
    tokensPercent := float64(tokens) / 128000 * 100
    return tokensPercent >= 30 || m.countStaleOutputs(messages) > 5
}

func (m *MiniCompact) Compact(messages []AgentMessage, _ string) (*CompactionResult, error) {
    // 只截断，不总结
    truncated := m.truncateStaleOutputs(messages)
    summary := fmt.Sprintf("Truncated %d stale tool outputs", m.countTruncated(messages))
    
    return &CompactionResult{
        Summary:      summary,
        Messages:     truncated,
        TokensBefore: m.EstimateContextTokens(messages),
        TokensAfter:  m.EstimateContextTokens(truncated),
    }, nil
}
```

### 方案 3: 在 LoopConfig 中选择实现

```go
// LoopConfig
type LoopConfig struct {
    // ... 现有字段
    
    // Compactor 可以是：
    // 1. compact.Compactor（完整的 LLM 总结）
    // 2. MiniCompact（轻量级截断）
    // 3. nil（禁用）
    Compactor Compactor
    
    // 或者同时支持两个
    MainCompactor      Compactor  // 完整压缩
    MiniCompactor      Compactor  // 轻量级截断
    
    // 触发配置
    MiniCompactEnabled  bool     // 启用 mini compact
    FullCompactEnabled bool     // 启用 full compact
}
```

## 推荐

**方案 2**：创建 `MiniCompact` 实现 `Compactor` 接口

优点：
1. 不破坏现有接口
2. `LoopConfig` 中无需修改
3. `runInnerLoop` 中可以直接使用
4. 易于测试（实现了同一接口）

集成方式：
```go
// LoopConfig
Compactor Compactor  // 可以是 FullCompact 或 MiniCompact

// runInnerLoop 中
if config.Compactor.ShouldCompact(agentCtx.Messages) {
    result, err := config.Compactor.Compact(agentCtx.Messages, agentCtx.LastCompactionSummary)
    // 处理结果...
}
```

这样我们不需要引入新的 `ContextManager` 接口，直接复用 `Compactor` 即可。
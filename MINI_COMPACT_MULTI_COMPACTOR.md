# 多 Compactor 并存设计

## 接口统一

所有 compact 都实现 `Compactor` 接口：

```go
type Compactor interface {
    ShouldCompact(messages []AgentMessage) bool
    Compact(messages []AgentMessage, previousSummary string) (*CompactionResult, error)
    CalculateDynamicThreshold() int
    EstimateContextTokens(messages []AgentMessage) int
}
```

## 多个实现

```go
// FullCompact - 完整压缩（LLM 总结）
type FullCompact struct {
    config *compact.Config
    model  llm.Model
    apiKey string
    // ... LLM 相关
}

// MiniCompact - 轻量截断（仅截断，不总结）
type MiniCompact struct {
    config *MiniCompactConfig
    truncater TruncationStrategy
}
```

## LoopConfig 配置

```go
type LoopConfig struct {
    // 支持多个 Compactor，按优先级执行
    Compactors []Compactor
    
    // 或者单独配置
    FullCompactor  Compactor  // 完整压缩
    MiniCompactor  Compactor  // 轻量截断
}
```

## 执行逻辑

```go
// runInnerLoop 中
for _, c := range config.Compactors {
    if c.ShouldCompact(agentCtx.Messages) {
        slog.Info("Compaction triggered", "compactor", typeName(c))
        result, err := c.Compact(agentCtx.Messages, agentCtx.LastCompactionSummary)
        // 处理结果...
        break // 第一个触发就停止
    }
}
```

## 优势

1. ✅ **接口统一** — 都实现 `Compactor`
2. ✅ **实现分离** — `FullCompact` 和 `MiniCompact` 各自实现
3. ✅ **优先级控制** — 通过数组顺序控制优先级
4. ✅ **易于扩展** — 添加新的 compact 实现
5. ✅ **灵活配置** — 可以只启用一种，或两种都启用

## 配置示例

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
# Mini Compact 设计方案

## 背景

1. **Checkpoint**（数据层）
   - checkpoint/journal
   - 对应旧的 session 机制
   - **不改动这部分**

2. **Mini Compact**（逻辑层）
   - Context Management 重构
   - 对应旧的 task_tracking + context_management 工具
   - **这部分是重点**

## 设计目标

像 `compact.Compactor` 一样设计成干净的接口，支持多种实现：

```
pkg/context/manager.go
├── ContextManager 接口（核心）
└── ContextManagementConfig 配置
```

## 接口设计

### ContextManager（核心接口）

```go
type ContextManager interface {
    // 判断是否需要触发 context management
    ShouldTrigger(ctx context.Context, messages []AgentMessage) (bool, urgency, reason)

    // 执行 context management
    Execute(ctx context.Context, messages []AgentMessage) ([]AgentMessage, summary, error)

    // 返回系统 prompt
    BuildSystemPrompt() string
}
```

### 实现

```go
// Mini Compact 实现（轻量级）
type miniContextManager struct {
    config    *ContextManagementConfig
    truncater TruncationStrategy
}

// Full Compact 实现（完整）
type fullContextManager struct {
    config     *ContextManagementConfig
    summarizer LLMSummarizer
    truncater  TruncationStrategy
}
```

## 与现有系统集成

### Agent 层

```go
// LoopConfig 添加
ContextManager context.ContextManager

// runInnerLoop 中使用
if shouldTrigger, urgency, reason := config.ContextManager.ShouldTrigger(...) {
    slog.Info("Context management triggered", "urgency", urgency, "reason", reason)
    messages, summary, err := config.ContextManager.Execute(...)
}
```

### Prompt 层

```go
// Builder 支持设置 system prompt
builder.SetContextManagerSystemPrompt(prompt.MiniCompactSystemPrompt())
```

## 优势

1. **接口清晰** — 像 Compactor 一样，支持多种实现
2. **易于扩展** — 可以添加新的 ContextManager 实现
3. **不耦合** — 与 loop 分离，可以独立测试
4. **可配置** — 通过配置文件选择实现
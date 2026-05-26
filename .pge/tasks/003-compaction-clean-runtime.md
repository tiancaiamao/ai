# Task 003: Compaction cleans runtime_state messages

## Goal
在 compaction 流程中，cache-first 模式下清理所有旧的 Kind="runtime_state" 消息，只保留最新一条。Context-first 模式下不需要此行为（因为没有持久化的 runtime_state）。

## Files (scope)
- `pkg/compact/compact.go` — 修改 compaction 逻辑
- `pkg/compact/compact_test.go` — 追加测试

## Estimated Size
S (<100 lines)

## Dependencies
Task 002 (runtime_state 持久化)

## Task Details

### Compaction 入口

在 compaction 替换消息时，过滤旧的 runtime_state 消息：

```go
func cleanOldRuntimeState(messages []agentctx.AgentMessage) []agentctx.AgentMessage {
    // 找到最后一条 runtime_state
    lastRuntimeIdx := -1
    for i := len(messages) - 1; i >= 0; i-- {
        if messages[i].Kind == "runtime_state" {
            lastRuntimeIdx = i
            break
        }
    }

    // 移除所有 runtime_state，保留最后一条
    var result []agentctx.AgentMessage
    for i, msg := range messages {
        if msg.Kind == "runtime_state" && i != lastRuntimeIdx {
            continue
        }
        result = append(result, msg)
    }
    return result
}
```

这个函数只在 cache-first 模式下调用。Compaction 本身已经 cache miss，清理旧 runtime_state 不增加额外代价。

### 测试

1. **TestCompactionCleansRuntimeState**:
   - 构造 5 条 runtime_state + 其他消息
   - 调用 cleanOldRuntimeState
   - 断言只剩 1 条 runtime_state（最新的那条）
   - 断言其他消息不受影响

## Acceptance
- `go build ./...` 通过
- spec.md AS-4 验证通过
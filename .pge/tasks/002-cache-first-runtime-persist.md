# Task 002: Implement cache-first runtime_state persistent append

## Goal
修改 `streamAssistantResponse` 中的 runtime_state 注入逻辑。当 CacheMode 为 cache 时，runtime_state 作为持久化 AgentMessage 追加到 RecentMessages；当 CacheMode 为 context 时，保持当前行为（临时注入 insertBeforeLastUserMessage）。

## Files (scope)
- `pkg/agent/llm_stream.go` — 修改 injectRuntimeMeta 使用路径
- `pkg/agent/llm_stream_test.go` — 新建/追加测试

## Estimated Size
M (150-300 lines)

## Dependencies
Task 001 (CacheMode types and MessageMutationPolicy)

## Task Details

### 当前代码路径（llm_stream.go:53-61）

```go
runtimeAppendix := injectRuntimeMeta(agentCtx, config)
if runtimeAppendix != "" {
    runtimeMsg := llm.LLMMessage{
        Role:    "user",
        Content: runtimeAppendix,
    }
    llmMessages = insertBeforeLastUserMessage(llmMessages, runtimeMsg)
}
```

### 新代码路径

```go
// Resolve cache mode
mode := config.CacheMode
if mode == CacheModeAuto {
    mode = IsCacheMode(config.Model.ID)
}
policy := DefaultMutationPolicy(mode)

runtimeAppendix := injectRuntimeMeta(agentCtx, config)
if runtimeAppendix != "" {
    switch policy.RuntimeStateStrategy() {
    case RuntimeStatePersist:
        // Cache-first: 创建持久化 AgentMessage，追加到 RecentMessages
        runtimeAgentMsg := agentctx.AgentMessage{
            Role:    "user",
            Content: []agentctx.ContentBlock{{Type: "text", Text: runtimeAppendix}},
            Kind:    "runtime_state",
        }
        agentCtx.RecentMessages = append(agentCtx.RecentMessages, runtimeAgentMsg)
        // 重新从 RecentMessages 构建 LLM messages（包含新的 runtime_state）
        selectedMessages, _ = selectMessagesForLLM(agentCtx)
        llmMessages = ConvertMessagesToLLM(ctx, selectedMessages)
    case RuntimeStateEphemeral:
        // Context-first: 当前行为不变
        runtimeMsg := llm.LLMMessage{
            Role:    "user",
            Content: runtimeAppendix,
        }
        llmMessages = insertBeforeLastUserMessage(llmMessages, runtimeMsg)
    }
}
```

### 关键约束

1. **`Kind: "runtime_state"`** — 用于标识这类消息，compaction 时可识别并清理
2. **持久化后的 runtime_state 消息通过正常 `ConvertMessagesToLLM` 路径序列化** — 不需要特殊处理
3. **Session journal 层面** — runtime_state 消息会随 RecentMessages 一起持久化，这是预期行为
4. **不要修改 `injectRuntimeMeta` 函数本身** — 它只负责计算内容，不负责注入策略

### 测试

1. **TestCacheFirstRuntimeStatePersist**:
   - 设置 CacheMode=Cache，mock AgentContext
   - 调用 3 轮 streamAssistantResponse（mock LLM 返回简单回复）
   - 每轮检查 RecentMessages 中新增 Kind="runtime_state" 消息
   - 序列化每轮的 []llm.LLMMessage，对前 N-2 条做 bytes.Equal

2. **TestContextFirstRuntimeStateEphemeral**:
   - 设置 CacheMode=Context
   - 验证 RecentMessages 中不含 Kind="runtime_state" 消息
   - 验证临时注入仍然通过 insertBeforeLastUserMessage

3. **TestPrefixConsistency**:
   - 10 轮对话 cache-first
   - 每轮的 []llm.LLMMessage 前缀逐轮单调增长且已有部分不变

## Acceptance
- `go build ./...` 通过
- spec.md AS-1, AS-2, AS-5 验证通过
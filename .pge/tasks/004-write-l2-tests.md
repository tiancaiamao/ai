# Task 004: Write L2 Behavioral Tests

## Context
Read .pge/spec.md and .pge/state.md for context. 只实现测试代码，不需要写功能代码。确保 `go build ./...` 和 `go test ./pkg/agent/... ./pkg/compact/...` 通过。

## Goal
Write 5 L2 behavioral test functions as specified in .pge/spec.md acceptance criteria.

## Test Files to Create

### 1. `pkg/agent/cache_policy_test.go` — Tests AS-3

**TestAutoCacheModeDetection**:
- `IsCacheMode("deepseek-chat")` → `CacheModeCache`
- `IsCacheMode("DeepSeek-Reasoner")` → `CacheModeCache` (case-insensitive)
- `IsCacheMode("glm-4")` → `CacheModeContext`
- `IsCacheMode("")` → `CacheModeContext`
- `IsCacheMode("claude-3-opus")` → `CacheModeContext`

**TestResolveCacheMode**:
- `ResolveCacheMode(CacheModeAuto, "deepseek-chat")` → `CacheModeCache`
- `ResolveCacheMode(CacheModeAuto, "glm-4")` → `CacheModeContext`
- `ResolveCacheMode(CacheModeCache, "glm-4")` → `CacheModeCache` (explicit override)
- `ResolveCacheMode(CacheModeContext, "deepseek-chat")` → `CacheModeContext` (explicit override)

**TestDefaultMutationPolicy**:
- `DefaultMutationPolicy(CacheModeCache).RuntimeStateStrategy()` → `RuntimeStatePersist`
- `DefaultMutationPolicy(CacheModeContext).RuntimeStateStrategy()` → `RuntimeStateEphemeral`
- `DefaultMutationPolicy(CacheModeAuto).RuntimeStateStrategy()` → `RuntimeStateEphemeral` (fallback)

### 2. `pkg/agent/llm_stream_test.go` — Tests AS-1, AS-2, AS-5

These tests verify the runtime_state injection behavior in `streamAssistantResponse`. However, `streamAssistantResponse` requires many dependencies (LLM stream, event stream, etc.) that make direct testing difficult. Instead, test the **observable effects** by testing the isolated logic:

**Approach**: Test the runtime_state injection logic at the unit level by replicating the decision logic. The key code paths are:

For AS-1 and AS-2, the core logic to test is:
```go
// In streamAssistantResponse, when runtimeAppendix != "":
if policy.RuntimeStateStrategy() == RuntimeStatePersist {
    // AS-1: append AgentMessage{Kind:"runtime_state"} to RecentMessages, rebuild llmMessages
} else {
    // AS-2: ephemeral insertBeforeLastUserMessage, NO change to RecentMessages
}
```

Since `streamAssistantResponse` is a private function that requires a full LLM setup, write tests that verify:

**TestCacheFirstRuntimeStatePersist** (AS-1):
- Create an `AgentContext` with some existing `RecentMessages`
- Simulate the cache-first path: create `AgentMessage{Role:"user", Metadata: &MessageMetadata{Kind:"runtime_state"}, Content: ...}` and append to `RecentMessages`
- Verify: `RecentMessages` has the new message appended (length increased by 1)
- Verify: The appended message has `Metadata.Kind == "runtime_state"`
- Verify: After 3 simulated "turns" (3 appends), all 3 runtime_state messages are in RecentMessages
- Verify: Serializing the messages to `[]llm.LLMMessage` via `selectMessagesForLLM` + `ConvertMessagesToLLM`, the prefix before each turn's new messages is stable (bytes.Equal)

**TestContextFirstRuntimeStateEphemeral** (AS-2):
- Create an `AgentContext` with some existing `RecentMessages`
- Call `insertBeforeLastUserMessage` with a runtime message (context-first path)
- Verify: `RecentMessages` is UNCHANGED (no new persistent message)
- Verify: The returned `[]llm.LLMMessage` has the runtime message inserted before the last user message

**TestPrefixConsistency** (AS-5):
- Simulate 10 turns of cache-first mode
- Each turn: append user message → append runtime_state message → append assistant message
- After each turn, call `selectMessagesForLLM` + `ConvertMessagesToLLM`
- Verify: Turn N's messages[:len(Turn(N-1))] equals Turn N-1's full messages (prefix is stable)
- Verify: Each turn's message count strictly increases (monotonic growth)

### 3. `pkg/compact/compact_test.go` or `pkg/compact/clean_runtime_state_test.go` — Test AS-4

**TestCompactionCleansRuntimeState** (AS-4):
- Test `cleanOldRuntimeState` directly (it's a package-level function)
- Case 1: No runtime_state messages → unchanged
- Case 2: 1 runtime_state → kept
- Case 3: 3 runtime_state → only last kept
- Case 4: Interleaved runtime_state and user messages → only runtime_state cleaned, others preserved
- Case 5: All messages are runtime_state → only last kept
- Case 6: Empty slice → empty result
- Case 7: Messages with nil Metadata → not affected

## Key Types/Functions Reference
- `agentctx.AgentMessage` — has `Role string`, `Content []ContentBlock`, `Metadata *MessageMetadata`, `Timestamp int64`
- `agentctx.MessageMetadata` — has `Kind string`
- `agentctx.NewUserMessage(text string) AgentMessage`
- `agentctx.NewAssistantMessage() AgentMessage`
- `agentctx.TextContent{Type: "text", Text: "..."}`
- `selectMessagesForLLM(agentCtx) ([]AgentMessage, string)` — returns `agentCtx.RecentMessages`
- `ConvertMessagesToLLM(ctx, messages) []llm.LLMMessage` — converts to LLM format
- `insertBeforeLastUserMessage(messages []llm.LLMMessage, msg llm.LLMMessage) []llm.LLMMessage`
- `cleanOldRuntimeState(messages []agentctx.AgentMessage) []agentctx.AgentMessage` — in pkg/compact
- `llm.LLMMessage` — has `Role string`, `Content string`

## Constraints
- Use only `testing` and `github.com/stretchr/testify/assert` for assertions
- Do NOT modify any non-test source files
- All tests must pass: `go test ./pkg/agent/... ./pkg/compact/... -v`
- Build must pass: `go build ./...`
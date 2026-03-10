# Tasks: Refactor LLM Context Interaction Protocol

## Phase 1: Core Tool Implementation

- [x] **T1.1**: Create `pkg/tools/llm_context_update.go`
  - Define `LLMContextUpdateTool` struct
  - Implement `Name()` returning `"llm_context_update"`
  - Implement `Description()` with usage guidance
  - Implement `Parameters()` with `content` string parameter

- [x] **T1.2**: Implement `Execute()` method
  - Extract `content` from params
  - Call `llmContext.WriteContent(content)` for dual-write
  - Return `[]ContentBlock{TextContent{Type: "text", Text: "Context updated."}}`
  - Add traceevent logging for observability

- [x] **T1.3**: Register tool in `cmd/ai/rpc_handlers.go`
  - Add `tools.NewLLMContextUpdateTool(llmContext)` to registry

- [x] **T1.4**: Register tool in `cmd/ai/headless_mode.go`
  - Same as above

## Phase 2: Prompt Template Update

- [x] **T2.1**: Update `pkg/prompt/llm_context.md`
  - Change step 3: `update overview.md` → `call llm_context_update tool`
  - Change External Memory: `Auto-injected each turn` → `Persisted context state. Restored after compact.`
  - Change header: `When overview.md update is REQUIRED` → `When llm_context_update is REQUIRED`
  - Add Tool Guidelines section for `llm_context_update`

- [x] **T2.2**: Update `pkg/context/llm_context.go`
  - Update reminder text: `write tool` → `llm_context_update tool`
  - Update `GetOverviewTemplate()` to use new tool

## Phase 3: Injection Logic Modification

- [x] **T3.1**: Modify `pkg/context/context.go`
  - Add `PostCompactRecovery bool` field to `AgentContext` struct
  - Add comment explaining purpose: inject overview.md after compact for recovery

- [x] **T3.2**: Update `pkg/agent/loop.go`
  - After compact completes, set `agentCtx.PostCompactRecovery = true`
  - Modify `buildRuntimeAppendix()` to check `PostCompactRecovery` flag
  - When true, inject overview.md content for recovery
  - Reset `PostCompactRecovery = false` after injection

## Phase 4: Truncate Protection

- [x] **T4.1**: Add helper function in `pkg/tools/llm_context_decision.go`
  - `findLatestToolCall(messages []Message, toolName string) string`
  - Iterate messages in reverse, find latest tool call by name

- [x] **T4.2**: Modify `filterAlreadyTruncated()` function
  - Call `findLatestToolCall(messages, "llm_context_update")` to get protected ID
  - Skip truncation if `msg.ToolCallID == protectedID`

## Phase 5: Testing

- [x] **T5.1**: Unit test for `llm_context_update` tool
  - Test tool returns `"Context updated."`
  - Test file is written via mock or temp directory

- [x] **T5.2**: Unit test for truncate protection
  - Create messages with multiple tool calls including `llm_context_update`
  - Verify latest `llm_context_update` is not in truncate list

- [x] **T5.3**: Run all existing tests
  - `go test ./...` to ensure no regressions

## Phase 6: Documentation

- [x] **T6.1**: Update `docs/llm-context/README.md`
  - Change `write`/`read` → `llm_context_update`

- [x] **T6.2**: Update `docs/llm-context/design.md`
  - Change "每次请求自动注入" → "compact 后注入"

- [x] **T6.3**: Update `docs/llm-context/IMPLEMENTATION_SUMMARY.md`
  - Update injection description

- [x] **T6.4**: Update `docs/llm-context/feedback-loop-design.md`
  - Change `write tool` → `llm_context_update tool` (3处)

## Completion Checklist

- [x] All tests pass
- [x] Manual testing: LLM can call `llm_context_update`
- [x] Manual testing: `overview.md` not injected in normal requests
- [x] Manual testing: `overview.md` injected after compact
- [x] Manual testing: Latest `llm_context_update` survives truncate
- [x] Documentation updated

## Implementation Notes

### Deviations from Plan

1. **T3.1 Location Change**: Added `PostCompactRecovery` to `pkg/context/context.go` instead of `pkg/prompt/builder.go`
   - Rationale: Cleaner to keep flag in AgentContext, closer to where it's used

2. **T4.2 Method Change**: Modified `filterAlreadyTruncated()` instead of `processTruncate()`
   - Rationale: Protection logic belongs in filter function

### Additional Work

1. **T2.2**: Added - Update `pkg/context/llm_context.go` reminder text and template
2. **Phase 6**: Added - Documentation updates for `docs/llm-context/`
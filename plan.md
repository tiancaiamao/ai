# Plan: Refactor LLM Context Interaction Protocol

## Overview

实现 `llm_context_update` 工具，修改 prompt 注入逻辑，让 LLM context 管理更符合 codex 模式。

## Technical Context

### Key Files

| 文件 | 作用 |
|------|------|
| `pkg/context/llm_context.go` | LLMContext 管理，包含 `WriteContent()` 方法 |
| `pkg/prompt/builder.go` | `buildLLMContextSection()` 构建 prompt 注入 |
| `pkg/prompt/llm_context.md` | 注入的 prompt 模板 |
| `pkg/tools/llm_context_decision.go` | 现有 truncate/compact 工具 |

### Current Flow

```
Request → builder.buildLLMContextSection() → inject overview.md content to prompt
         ↓
         LLM sees overview.md in system prompt
         ↓
         LLM uses write tool to update overview.md
         ↓
         tool output recorded in messages
```

### New Flow

```
Request → builder.buildLLMContextSection() → NO injection (unless post-compact)
         ↓
         LLM sees llm_context.md prompt template only
         ↓
         LLM calls llm_context_update tool
         ↓
         tool output recorded + WriteContent() to overview.md
         ↓
         (after compact) inject overview.md for recovery
```

## Implementation Plan

### Task 1: Create `llm_context_update` Tool

**File**: `pkg/tools/llm_context_update.go`

```go
type LLMContextUpdateTool struct {
    llmContext *context.LLMContext
}

func (t *LLMContextUpdateTool) Name() string {
    return "llm_context_update"
}

func (t *LLMContextUpdateTool) Parameters() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "content": map[string]any{
                "type": "string",
                "description": "Markdown content to record (task, decisions, known info, pending)",
            },
        },
        "required": []string{"content"},
    }
}

func (t *LLMContextUpdateTool) Execute(ctx context.Context, params map[string]any) ([]ContentBlock, error) {
    content := params["content"].(string)

    // Dual-write:
    // 1. Write to overview.md file
    t.llmContext.WriteContent(content)

    // 2. Return simple confirmation (tool output stays in context)
    return []ContentBlock{TextContent{Type: "text", Text: "Context updated."}}, nil
}
```

**Registration**: Add to `cmd/ai/rpc_handlers.go` and `cmd/ai/headless_mode.go`

### Task 2: Update `llm_context.md` Prompt Template

**File**: `pkg/prompt/llm_context.md`

Minimal changes:
1. Turn Protocol step 3: `update overview.md` → `call llm_context_update tool`
2. External Memory: Remove "Auto-injected each turn"
3. Add Tool Guidelines section for `llm_context_update`

### Task 3: Modify Prompt Injection Logic

**File**: `pkg/prompt/builder.go`

Current:
```go
func (b *Builder) buildLLMContextSection() string {
    // Always injects overview.md content
}
```

New:
```go
func (b *Builder) buildLLMContextSection() string {
    // Only inject llm_context.md template
    // Do NOT inject overview.md content (unless post-compact flag set)
}
```

Add `SetPostCompact(bool)` to Builder to enable injection after compact.

### Task 4: Truncate Protection for Latest Update

**File**: `pkg/tools/llm_context_decision.go`

Modify `processTruncate()` to skip the most recent `llm_context_update` tool output:

```go
func (t *LLMContextDecisionTool) processTruncate(...) int {
    // Find latest llm_context_update tool call ID
    latestUpdateID := findLatestToolCall(messages, "llm_context_update")

    for i := range agentCtx.Messages {
        // Skip if this is the latest llm_context_update
        if msg.ToolCallID == latestUpdateID {
            continue
        }
        // ... existing truncate logic
    }
}
```

### Task 5: Compact Recovery Injection

**File**: `pkg/agent/loop.go` or `pkg/compact/compact.go`

After compact completes, set flag to inject `overview.md` on next request:

```go
// In compact handler
if compacted {
    agentCtx.PostCompactRecovery = true
}

// In prompt builder
if b.postCompactRecovery {
    // Inject overview.md content
    content, _ := b.llmContext.Load()
    // Add to prompt
}
```

## Dependencies

```
Task 1 (new tool) ─────────────────────────────────────┐
                                                        │
Task 2 (prompt template) ───────────────────────────────┤
                                                        │
Task 3 (injection logic) ───────────────────────────────┤
                                                        │
Task 4 (truncate protection) ───────────────────────────┤
                                                        │
Task 5 (compact recovery) ──────────────────────────────┘
```

All tasks are independent and can be implemented in parallel.

## Testing Strategy

1. **Unit test for `llm_context_update`**:
   - Verify tool returns `"Context updated."`
   - Verify file is written via `WriteContent()`

2. **Unit test for truncate protection**:
   - Create messages with multiple `llm_context_update` calls
   - Verify latest is not truncated

3. **Integration test**:
   - Simulate full flow: update → compact → recovery injection

## Risks

| Risk | Mitigation |
|------|------------|
| LLM continues using `write` to update overview.md | Prompt explicitly says to use `llm_context_update` |
| Compact recovery not triggered | Add explicit flag in AgentContext |
| Truncate protection misses edge cases | Log when protecting, add tests |

## Acceptance Criteria

- [ ] `llm_context_update` tool registered and working
- [ ] Tool performs dual-write (file + tool output)
- [ ] `overview.md` not auto-injected in normal requests
- [ ] `overview.md` injected after compact for recovery
- [ ] Latest `llm_context_update` protected from truncate
- [ ] All tests pass
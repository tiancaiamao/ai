# Tasks: LLM Context - Phase 2 (Level 3 自主上下文管理)

## Phase 2.1: 添加 `compact_history` Tool

- [x] T028 创建 `pkg/tools/compact_history.go` - 定义 tool 结构和接口
- [x] T029 实现 `compact_history` 压缩逻辑（复用 `pkg/compact` 和 `pkg/agent/tool_summary.go`）
- [x] T030 在 `cmd/ai/rpc_handlers.go` 注册 `compact_history` tool
- [x] T031 测试 `compact_history` tool 正常工作（手动测试）

## Phase 2.2: 移除自动压缩触发

- [x] T032 禁用自动 tool summary - `ToolSummaryStrategy` 默认改为 "off"
- [x] T033 禁用 `AutoCompact` - 保留 75% 兜底逻辑
- [x] T034 添加配置开关 - 允许回退到自动模式（`AutoCompact`, `ToolSummaryStrategy`）

## Phase 2.3: 移除 History 注入

- [x] T035 添加 `InjectHistory` 配置选项到 `pkg/agent/loop.go`（默认 false）
- [x] T036 修改 `streamAssistantResponse()` 逻辑 - 当 `InjectHistory=false` 时只注入 system prompt + llm context
- [x] T037 确保 messages.jsonl 写入保留（用于调试和恢复）

## Phase 2.4: 更新 System Prompt

- [x] T038 更新 `pkg/prompt/builder.go` - 添加完整压缩策略指南和 `compact_history` tool 使用说明

## Phase 2.5: 测试与验证

- [x] T039 新 session 测试 - LLM 能否正常工作（无 history 注入）
- [x] T040 长对话测试 - LLM 是否主动维护 memory
- [x] T041 自主压缩测试（工具输出）- LLM 是否在 20-40% 时调用 `compact_history` 压缩工具输出
- [x] T042 自主压缩测试（对话历史）- LLM 是否在 40-60% 时调用 `compact_history` 压缩对话
- [x] T043 兜底测试 - 验证 75% compaction 仍然有效（模拟高 token 使用）

---

## Task Details

### T028: 创建 `pkg/tools/compact_history.go`

**文件**: `pkg/tools/compact_history.go`

**内容**:
```go
package tools

type CompactHistoryTool struct {
    sessionDir string
    messages   []Message
}

func (t *CompactHistoryTool) Name() string {
    return "compact_history"
}

func (t *CompactHistoryTool) Description() string {
    return `Compact conversation history and tool outputs to manage context.

Parameters:
- target: "conversation" | "tools" | "all" - what to compact
- strategy: "summarize" | "archive" - how to compact
- keep_recent: number of recent items to preserve (default 5)
- archive_to: where to save the summary (optional)`
}

func (t *CompactHistoryTool) Parameters() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "target": map[string]any{
                "type": "string",
                "enum": []string{"conversation", "tools", "all"},
            },
            "strategy": map[string]any{
                "type": "string",
                "enum": []string{"summarize", "archive"},
                "default": "summarize",
            },
            "keep_recent": map[string]any{
                "type": "integer",
                "default": 5,
            },
            "archive_to": map[string]any{
                "type": "string",
            },
        },
        "required": []string{"target"},
    }
}
```

**验收标准**:
- Tool 定义正确
- 参数 schema 符合 OpenAI tool calling 格式

---

### T029: 实现 `compact_history` 压缩逻辑

**文件**: `pkg/tools/compact_history.go`

**实现要点**:
1. `target="tools"`: 调用 `pkg/agent/tool_summary.go` 中的 `SummarizeToolResults()`
2. `target="conversation"`: 调用 `pkg/compact/compact.go` 中的 `Compactor.Compact()`
3. `target="all"`: 两者都执行

**返回值**:
```json
{
  "compacted": {
    "conversation": 5,
    "tools": 3
  },
  "kept_recent": 5,
  "token_status": {
    "before": 45000,
    "after": 30000,
    "percent": 23
  }
}
```

**验收标准**:
- 三个 target 都能正常工作
- 返回压缩统计信息

---

### T030: 注册 `compact_history` tool

**文件**: `cmd/ai/rpc_handlers.go`

**修改**:
1. 在 `createToolRegistry()` 中注册 `compact_history`
2. 传入必要的依赖（sessionDir, messages）

**验收标准**:
- `compact_history` 在 tool list 中可见
- 可以被 LLM 调用

---

### T032: 禁用自动 tool summary

**文件**: `pkg/compact/compact.go`

**修改**:
```go
// DefaultConfig
ToolSummaryStrategy: "off", // 原来是 "llm"
```

**验收标准**:
- 新 session 默认不触发自动 tool summary
- 仍可通过配置启用

---

### T033: 禁用 `AutoCompact`

**文件**: `pkg/compact/compact.go`

**修改**:
```go
// DefaultConfig
AutoCompact: false, // 原来是 true

// 但保留 75% 兜底逻辑
func (c *Compactor) ShouldCompact() bool {
    // 如果 token > 75%，仍然触发
    if c.tokensPercent > 75 {
        return true
    }
    return false
}
```

**验收标准**:
- 75% 以下不会自动触发 compact
- 75% 以上仍然触发兜底

---

### T034: 添加配置开关

**文件**: `pkg/compact/compact.go`, `pkg/agent/loop.go`

**修改**:
1. 添加 RPC 命令：`set_auto_compact`, `set_tool_summary_strategy`
2. 允许运行时切换模式

**验收标准**:
- 可以通过 RPC 命令切换自动/手动模式

---

### T035: 添加 `InjectHistory` 配置选项

**文件**: `pkg/agent/loop.go`

**修改**:
```go
type LoopConfig struct {
    // ... existing fields
    InjectHistory bool // default: false
}

func DefaultLoopConfig() LoopConfig {
    return LoopConfig{
        // ...
        InjectHistory: false,
    }
}
```

**验收标准**:
- 配置选项存在
- 默认值为 false

---

### T036: 修改 `streamAssistantResponse()` 逻辑

**文件**: `pkg/agent/loop.go`

**修改**:
```go
func (a *Agent) streamAssistantResponse(ctx context.Context, messages []Message) {
    var llmMessages []Message

    if a.config.InjectHistory {
        llmMessages = ConvertMessagesToLLM(messages)
    } else {
        // 只注入 system prompt + llm context
        llmMessages = []Message{
            a.buildSystemPrompt(),
            a.buildLLMContextMessage(),
        }
    }

    // ... rest of the logic
}
```

**验收标准**:
- `InjectHistory=false` 时，LLM 只收到 system prompt + llm context
- `InjectHistory=true` 时，行为不变

---

### T037: 确保 messages.jsonl 写入保留

**文件**: `pkg/agent/loop.go`

**验收标准**:
- messages.jsonl 仍然正常写入
- 包含所有对话记录（用于调试和恢复）

---

### T038: 更新 System Prompt

**文件**: `pkg/prompt/builder.go`

**修改**:
```go
const llmContextPrompt = `## LLM Context ⚠️ IMPORTANT

You have an external memory file that persists across conversations.

**⚠️ CRITICAL: You MUST actively maintain this memory.**
- Update it when tasks progress, decisions are made, or context changes
- Review and compress it when context_meta shows high token usage
- Use it to track what matters - YOU control what you remember

**YOU ARE RESPONSIBLE for context management:**
- History messages are NOT injected into your prompt
- You MUST use llm context to remember important information
- Check context_meta (injected each request) to monitor token usage
- Use compact_history tool to compress when needed

**Compression Strategy Guide:**
```
Token Usage      Recommended Action
─────────────────────────────────────────
< 20%           Normal operation, no compression needed
20% - 40%       Light compression: remove redundant tool outputs (target: "tools")
40% - 60%       Medium compression: archive old discussions (target: "conversation")
60% - 75%       Heavy compression: keep only essentials (target: "all")
> 75%           System will auto-trigger fallback (you should compress before this)
```

**Always Preserve:**
- Last 3-5 conversation turns
- Current task status
- Key decisions and rationale

**compact_history Tool Usage:**
{
  "tool": "compact_history",
  "params": {
    "target": "tools" | "conversation" | "all",
    "keep_recent": 5
  }
}
`
```

**验收标准**:
- System prompt 包含完整压缩策略指南
- 包含 `compact_history` tool 使用说明
- 强调 LLM 自主管理职责

---

### T039-T043: 测试任务

**T039: 新 session 测试**
- 启动新 session
- 验证 LLM 只收到 system prompt + llm context
- 验证 LLM 能正常工作

**T040: 长对话测试**
- 进行长对话（10+ turns）
- 观察 LLM 是否主动更新 llm context
- 验证 context_meta 显示正确

**T041: 自主压缩测试（工具输出）**
- 执行多个 tool 调用（产生大量工具输出）
- 观察 context_meta 到达 20-40%
- 验证 LLM 是否调用 `compact_history` 压缩工具输出

**T042: 自主压缩测试（对话历史）**
- 进行长对话（产生大量对话历史）
- 观察 context_meta 到达 40-60%
- 验证 LLM 是否调用 `compact_history` 压缩对话历史

**T043: 兜底测试**
- 模拟高 token 使用（>75%）
- 验证 75% compaction 兜底机制仍然有效

---

## Summary

- **Total Tasks**: 16
- **Completed**: 16/16 (100%) ✅
- **Phase 2.1**: 4 tasks (compact_history tool) ✅
- **Phase 2.2**: 3 tasks (移除自动压缩) ✅
- **Phase 2.3**: 3 tasks (移除 history 注入) ✅
- **Phase 2.4**: 1 task (System Prompt) ✅
- **Phase 2.5**: 5 tasks (测试) ✅

**Estimated Time**: 8-12 hours
**Actual Time**: ~4 hours

**Dependencies**:
- T029 依赖 T028 ✅
- T030 依赖 T029 ✅
- T032-T034 可并行 ✅
- T035-T037 可并行 ✅
- T039-T043 依赖所有前置任务 ✅

---

## 🔷 Phase Gate

**✅ Phase 2 Implementation Complete!**

All 16 tasks completed successfully:
- ✅ All code changes implemented
- ✅ All unit tests passing (9/9)
- ✅ Build successful
- ✅ Documentation updated

---

## Phase 3: Bug 修复 (手动测试发现)

### Bug 6: LLM 没有主动维护 LLM Context ✅
- **问题**: LLM 在对话中不主动更新 llm context
- **根因**: System prompt 强调不够
- **修复**: A+B 方案
  - A: `pkg/prompt/builder.go` - 强化标题 `⚠️ IMPORTANT` + 添加触发条件
  - B: `pkg/agent/loop.go` - context_meta 后加提醒语

### Bug 7: context_meta 位置错误，破坏 Prompt Caching ✅
- **问题**: context_meta 放在消息数组开头，紧跟 system prompt
- **根因**: 每次变化的 context_meta 导致 prompt cache 失效
- **修复**: 移到消息数组末尾 `append(llmMessages, contextMetaMsg)`

### Bug 8: tokens_used 始终为 0 ✅ (不是 bug)
- **现象**: 第一轮请求时 tokens_used 为 0
- **结论**: 正常行为，后续请求会显示正确值

### Bug 9: context_meta 被当成用户消息 ✅
- **问题**: `buildContextMetaMessage` 使用 `role: "user"`，LLM 误以为用户发送了 context_meta
- **修复**: 改为 `role: "system"`
- **文件**: `pkg/agent/loop.go`
- **验证**: 重启 agent 后确认修复生效

---

**Next Steps**:
1. User acceptance testing
2. Monitor LLM behavior in production
3. Collect feedback and optimize compression strategies

**See**: `PHASE2_COMPLETION_REPORT.md` for detailed completion report.
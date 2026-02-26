# Implementation Plan: Working Memory - Phase 2

## 概述

**Phase 1 已完成**：Working Memory 基础设施（目录结构、注入机制、LLM 维护提醒）

**Phase 2 目标**：实现核心架构变化——History Messages 被 Working Memory 取代

---

## 核心理念

**从 Level 0.5 → Level 3**：LLM 完全自主管理上下文

| 维度 | Phase 1（已完成） | Phase 2（目标） |
|------|-------------------|-----------------|
| Compaction 触发 | 代码写死 75% | **LLM 自己决定** |
| 压缩策略 | 固定规则 | **LLM 自主判断** |
| 上下文来源 | history + working memory | **只有** working memory + context_meta |
| History 用途 | 注入 prompt + 存储 | **只存储**，不注入 |

## 压缩策略指南（写入 System Prompt）

```
Token 使用量    建议操作
─────────────────────────────────────────
< 20%          正常工作，无需压缩
20% - 40%      轻度压缩：总结已完成任务，移除冗余
40% - 60%      中度压缩：归档详细讨论到 detail/，保留要点
60% - 75%      重度压缩：只保留关键决策和当前任务
> 75%          系统会自动触发兜底压缩（你应该在此之前主动压缩）

保留规则：
- 最近 3-5 条对话记录始终保留
- 当前任务状态必须保留
- 关键决策必须保留
```

**注意**：75% 时系统会强制触发 compaction 作为兜底。如果你主动维护 working memory，这层兜底应该不会被触发。

---

## 架构变化

### 消息注入流程

```
Phase 1（当前）:
System Prompt → Working Memory → History Messages → Context Meta

Phase 2（目标）:
System Prompt → Working Memory
                ↓
           LLM 自己决定:
           - 查看 context_meta（每次自动注入）
           - write 更新 memory
           - read 读取 detail/
```

### 关键变化

1. **History Messages 不再注入**
   - `messages.jsonl` 只用于存储和调试
   - LLM 必须通过 working memory 获取上下文

2. **Context Meta 变为 Tool** ❌ ~~改为保持自动注入~~
   - context_meta 仍然自动注入到消息末尾
   - LLM 每次都能看到，无需主动查询

3. **LLM 职责增强**
   - 必须主动维护 working memory
   - 必须在需要时查询上下文状态
   - 自己决定何时压缩/归档

---

## 新增组件

### 1. History 注入开关

```go
// pkg/agent/loop.go

type LoopConfig struct {
    // InjectHistory controls whether to inject history messages into prompt
    // Phase 2: default false (LLM uses working memory only)
    InjectHistory bool
}
```

### 2. `compact_history` Tool（核心工具）

让 LLM 完全自主管理上下文压缩：

```go
// pkg/tools/compact_history.go

type CompactHistoryTool struct {
    sessionDir string
    messages   []Message
}

func (t *CompactHistoryTool) Name() string {
    return "compact_history"
}

func (t *CompactHistoryTool) Description() string {
    return `Compact conversation history and tool outputs to manage context.

Usage:
{
  "target": "conversation" | "tools" | "all",
  "strategy": "summarize" | "archive",
  "keep_recent": 5,
  "archive_to": "working-memory/detail/session-summary.md"
}

Parameters:
- target: what to compact
  - "conversation": compact conversation history (user/assistant messages)
  - "tools": compact tool outputs (often large, lose value over time)
  - "all": compact both
- strategy: "summarize" creates a summary, "archive" moves to detail file
- keep_recent: number of recent items to preserve (default 5)
- archive_to: where to save the summary (optional, defaults to auto-generated name)

When to use:
- context_meta shows tokens > 20%: light compression (remove redundant tool outputs)
- context_meta shows tokens > 40%: medium compression (archive old discussions)
- context_meta shows tokens > 60%: heavy compression (keep only essentials)
- Always preserve: recent 3-5 turns, current task, key decisions

Returns: summary of what was compacted and current token status`
}
```

**调用示例**：

```json
// 压缩工具输出（20-40% 时）
{
  "tool": "compact_history",
  "params": {
    "target": "tools",
    "keep_recent": 3
  }
}

// 压缩对话历史（40-60% 时）
{
  "tool": "compact_history",
  "params": {
    "target": "conversation",
    "strategy": "archive",
    "keep_recent": 5,
    "archive_to": "working-memory/detail/task-progress.md"
  }
}

// 全面压缩（60-75% 时）
{
  "tool": "compact_history",
  "params": {
    "target": "all",
    "keep_recent": 5
  }
}
```

### 3. 移除自动压缩触发

**移除的机制**：
- ❌ `ToolCallCutoff` 自动 tool summary（`ToolSummaryStrategy` 改为 "off"）
- ❌ `AutoCompact` 自动触发（保留 75% 兜底）

**保留的机制**：
- ✅ 75% compaction 兜底（作为最后防线）
- ✅ Tool summary LLM 调用能力（供 `compact_history` tool 内部使用）

### 4. System Prompt 更新

```
## Working Memory ⚠️ IMPORTANT

You have an external memory file that persists across conversations.

**⚠️ CRITICAL: You MUST actively maintain this memory.**
- Update it when tasks progress, decisions are made, or context changes
- Review and compress it when context_meta shows high token usage
- Use it to track what matters - YOU control what you remember

**YOU ARE RESPONSIBLE for context management:**
- History messages are NOT injected into your prompt
- You MUST use working memory to remember important information
- Check context_meta (injected each request) to monitor token usage
- Compress and archive when needed

**Compression Strategy Guide:**
```
Token Usage      Recommended Action
─────────────────────────────────────────
< 20%           Normal operation, no compression needed
20% - 40%       Light compression: summarize completed tasks, remove redundancy
40% - 60%       Medium compression: archive details to detail/, keep key points
60% - 80%       Heavy compression: keep only key decisions and current task
> 80%           Emergency compression: compress now, prioritize recent context
```

**Always Preserve:**
- Last 3-5 conversation turns
- Current task status
- Key decisions and rationale

**File Path**: %s
**Detail Directory**: %s
```

---

## 实现步骤

### Phase 2.1: 添加 `compact_history` Tool

- [ ] T028 创建 `pkg/tools/compact_history.go`（支持 target: conversation/tools/all）
- [ ] T029 实现压缩逻辑（复用现有 `pkg/compact` 和 `pkg/agent/tool_summary.go`）
- [ ] T030 在 `cmd/ai/rpc_handlers.go` 注册 tool
- [ ] T031 测试 tool 正常工作

### Phase 2.2: 移除自动压缩触发

- [ ] T032 禁用自动 tool summary（`ToolSummaryStrategy` 默认改为 "off"）
- [ ] T033 禁用 `AutoCompact`（保留 75% 兜底逻辑）
- [ ] T034 添加配置开关（允许回退到自动模式）

### Phase 2.3: 移除 History 注入

- [ ] T035 添加 `InjectHistory` 配置选项（默认 false）
- [ ] T036 修改 `streamAssistantResponse()` 逻辑：
  - 当 `InjectHistory=false` 时，不调用 `ConvertMessagesToLLM`
  - 只注入 system prompt + working memory
- [ ] T037 保留 messages.jsonl 写入（用于调试和恢复）

### Phase 2.4: 更新 System Prompt

- [ ] T038 更新 `pkg/prompt/builder.go`：
  - 添加完整压缩策略指南（20%/40%/60%/75%）
  - 说明 `compact_history` tool 使用方法
  - 说明 75% 兜底机制
  - 强调 LLM 完全自主管理上下文

### Phase 2.5: 测试与验证

- [ ] T039 新 session 测试：LLM 能否正常工作
- [ ] T040 长对话测试：LLM 是否主动维护 memory
- [ ] T041 自主压缩测试（工具输出）：LLM 是否在 20-40% 时压缩工具输出
- [ ] T042 自主压缩测试（对话历史）：LLM 是否在 40-60% 时压缩对话
- [ ] T043 兜底测试：验证 75% compaction 仍然有效

---

## 风险与缓解

| 风险 | 缓解措施 |
|------|----------|
| LLM 忘记更新 memory | System prompt 强调 + 模板引导 |
| 上下文丢失 | messages.jsonl 保留，可恢复 |
| LLM 忽略 context_meta | 在提醒语中更明确说明 |
| 性能问题 | 可加 fallback：检测到 memory 太空时自动注入最近 N 条消息 |

---

## 回滚方案

如果 Phase 2 效果不佳，可以：

1. **配置开关**：设置 `InjectHistory=true` 回到 Phase 1 行为
2. **Hybrid 模式**：注入最近 N 条消息 + working memory
3. **渐进式**：先保留 context_meta 注入，等 LLM 学会后再移除

---

## 成功标准

- [ ] LLM 能够看到 context_meta（每次自动注入）
- [ ] LLM 能够使用 `compact_history` tool 主动压缩
- [ ] LLM 能够自主管理工具输出压缩（target: "tools"）
- [ ] LLM 能够自主管理对话历史压缩（target: "conversation"）
- [ ] LLM 在 75% 之前主动压缩（兜底不被触发）
- [ ] 长对话中 token 使用保持稳定（不会无限增长）
- [ ] 任务信息不丢失（关键决策、进度等记录在 memory 中）
- [ ] 75% 兜底机制仍然有效（作为最后防线）

---

## Open Questions

1. **Fallback 机制**：是否需要在 memory 太空时自动注入最近消息？
2. **Compaction 保留**：现有的自动 compaction 是否保留作为安全网？
3. **初始引导**：新 session 时是否需要给 LLM 一些示例？

---

## 🔷 Phase Gate

**Ready for task generation?**
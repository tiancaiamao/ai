# PLAN: Context Management Event-Log 模型（修正版）

## 设计文档对齐

设计文档（`design/context_snapshot_architecture.md`）已经明确了架构：

```
messages.jsonl ──apply logs───▶ ContextSnapshot ──render──▶ LLM Request
(immutable)      (incremental)
```

> Messages are stored as an immutable event log (messages.jsonl).
> Truncation operations are also events — they mark messages as truncated
> without deleting them. The ContextSnapshot is constructed by replaying all events.

现在的问题是**实现偏离了设计**：
- truncate 直接修改 RecentMessages 然后全量覆盖 messages.jsonl
- agent_end 全量覆盖 messages.jsonl
- checkpoint 存的是全量（没起到 snapshot 加速作用）

## 核心改动

### 改动 1: 定义 Compact Event Entry

mini compact 和 full compact 的操作都是 event，只是动作类型不同：
- `truncate` — 截断内容（mini compact 的 truncate_messages 工具）
- `update_llm_context` — 更新结构化上下文（mini compact 的 update_llm_context 工具）
- `compact` — 生成摘要替代旧消息（full compact）

**文件**: `pkg/session/entry.go`

```go
type CompactAction string
const (
    CompactActionTruncate         CompactAction = "truncate"
    CompactActionUpdateLLMContext CompactAction = "update_llm_context"
    CompactActionCompact          CompactAction = "compact"
)

// CompactEventDetail 记录一次 compact 操作。
// 只记录操作本身（which IDs, what action），不记录操作前后的状态。
// Apply 时根据操作类型重新执行逻辑即可（确定性）。
type CompactEventDetail struct {
    Action CompactAction `json:"action"`
    IDs    []string      `json:"ids,omitempty"` // truncate: 目标 tool_call_id 列表
}
```

**SessionEntry 扩展**：

```go
type SessionEntry struct {
    // ...existing fields...
    CompactEvent *CompactEventDetail `json:"compactEvent,omitempty"`
}
```

使用 `EntryTypeCompaction`（已有），不需要新增 entry type。

### 改动 2: truncate_messages 改为追加 event

**文件**: `pkg/tools/context_mgmt/truncate_messages.go`

当前行为：
```
1. filterValidIDs(ids)  → 找到可截断的消息
2. applyTruncate(ids)   → 直接修改 RecentMessages[i].Content
3. return               → loop 中 OnMessagesChanged → SaveMessages → 全量覆盖
```

改为：
```
1. filterValidIDs(ids)  → 找到可截断的消息
2. 追加一条 compact event 到 messages.jsonl:
   AppendCompactEvent({action: "truncate", ids: validIDs})
3. 在内存 apply（调用现有 applyTruncate，修改 RecentMessages[i].Content）
4. return → 不调用 OnMessagesChanged
```

**Apply 语义**：从上一个 snapshot 的基础上，对每个 ID：
- 找到 ToolCallID 匹配的 toolResult 消息
- 调用 TruncateWithHeadTail(originalText) 重新计算截断结果
- 替换 Content，标记 Truncated=true, TruncatedAt=currentTurn, OriginalSize=原长度
- 结果是确定性的，不需要在 event 中存储截断后的内容

### 改动 3: update_llm_context 也追加 event

**文件**: `pkg/tools/context_mgmt/update_llm_context.go`

当前行为：直接更新 agentCtx.LLMContext 字符串。

改为：
```
1. 更新内存 agentCtx.LLMContext
2. 通过 callback 追加 compact event:
   AppendCompactEvent(CompactEventDetail{
       Action: "update_llm_context",
   })
3. 不调用 OnMessagesChanged
```

Apply 语义：从 checkpoint 恢复时，llm_context.txt 已经是最新的。
如果没有 checkpoint，从 messages.jsonl replay 时，update_llm_context event 之前的
llm_context 变更已丢失（因为只存了最终结果在 llm_context.txt 中）。
所以 update_llm_context event 主要用于审计追踪。

### 改动 4: 去掉 OnMessagesChanged → SaveMessages 全量覆盖

**文件**: `cmd/ai/rpc_handlers.go`, `pkg/agent/loop.go`

- 删除 `ctx.OnMessagesChanged` 的赋值（`sess.SaveMessages`）
- 删除 loop.go 中所有 `OnMessagesChanged()` 调用
- compact event 通过 `AppendCompactEvent` 追加，不走 SaveMessages

### 改动 5: 去掉 agent_end 的 Replace

**文件**: `cmd/ai/rpc_handlers.go`

- 删除 `agent_end` 时的 `sessionWriter.Replace(sess, event.Messages)`
- turn 过程中的 Append 已经逐条追加了
- compact 的修改通过 AppendCompactEvent 追加了

**验证**：需要确认 `message_end` 和 `tool_execution_end` 的 Append 确实覆盖了
所有产生 AgentMessage 的事件类型。

### 改动 6: Checkpoint 存 Snapshot（apply 后的状态）

**文件**: `pkg/agent/checkpoint_manager.go`

CreateSnapshot 时存的是 apply 所有 compact event 后的 RecentMessages。
由于 truncate 不删条目只改内容，drop 如果以后加了会删条目，
所以 checkpoint 的条数 ≤ messages.jsonl 的条数。

当前 checkpoint 已经存 RecentMessages，如果 drop 实现了，
checkpoint 自然就只存存活的消息了。

### 改动 7: 恢复路径

**恢复路径 A（有 checkpoint）**：
```
LoadCheckpoint → 直接得到 snapshot（已 apply 过的 RecentMessages + AgentState + LLMContext）
```
这是快速路径，不需要 replay。

**恢复路径 B（无 checkpoint）**：
```
LoadMessages → 读所有 entries
  → 过滤出消息 entries（构建 RecentMessages）
  → replay compact events（apply truncate）
  → 得到 snapshot
```
这是慢路径，需要遍历并 apply。

**当前代码**（`rpc_handlers.go`）已经有这两条路径的逻辑，
只需要在路径 B 中加入 compact event 的 replay。

## 不加 drop_messages 的理由

**优点**：
- LLM 可以彻底移除无用消息，释放更多 token
- 配合 checkpoint 的 snapshot 语义，恢复更快

**缺点**：
- 增加复杂度：需要处理 drop 后的消息引用关系
- LLM 判断可能不准：误删有用的上下文
- truncate 已经足够：如果消息被截断到 head+tail（通常几百字符），
  它在 token 中占比已经很小。边际收益递减。
- 观察到的数据：687 条消息只截断了 2 条就省了 2700 tokens。
  如果能截断更多，效果已经够好了。

**建议**：先不加 drop_messages。先完成 event-log 模型 + truncate event，
观察实际效果。如果 truncate 的效果不够，再加 drop。

## 文件改动清单

| 文件 | 改动 |
|------|------|
| `pkg/session/entry.go` | 扩展 SessionEntry + CompactEventDetail 类型 |
| `pkg/session/session.go` | 新增 AppendCompactEvent 方法 |
| `pkg/tools/context_mgmt/truncate_messages.go` | 追加 event + 内存 apply |
| `pkg/tools/context_mgmt/update_llm_context.go` | 追加 event |
| `pkg/context/context.go` | 新增 OnCompactEvent callback |
| `cmd/ai/rpc_handlers.go` | 删除 OnMessagesChanged/Replace, 绑定 OnCompactEvent |
| `cmd/ai/session_writer.go` | 可删除 Replace 方法 |
| `pkg/agent/loop.go` | 删除 OnMessagesChanged 调用 |
| `pkg/agent/checkpoint_manager.go` | 确认存 apply 后的 snapshot |
| `pkg/session/session.go` GetMessages | 加 compact event replay 逻辑 |

## 测试计划

1. AppendCompactEvent 追加一行到 jsonl，不影响已有条目
2. compact event 的 apply：truncate 后内容变短，条数不变
3. 去掉 Replace 后，agent_end 不再覆盖文件
4. checkpoint 的消息是 apply 后的状态
5. 从 messages.jsonl（含 compact events）replay 出正确的 snapshot
6. 向后兼容：老 messages.jsonl（无 compact event）正常加载
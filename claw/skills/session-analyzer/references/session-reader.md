# Session Format Reference

## Journal 文件：messages.jsonl

Append-only journal，每行一个 JSON entry。共 5 种 entry type：

### 1. `session` — 头部标记

仅出现一次，在文件开头。

```json
{
  "type": "session",
  "version": 1,
  "id": "0f04fb49-a956-4bee-8cd6-595896fcd225",
  "timestamp": "2026-04-03T03:23:18.053792Z",
  "cwd": "/Users/genius/project/ai"
}
```

### 2. `session_info` — 会话信息

仅出现一次，在 session 之后。

```json
{
  "type": "session_info",
  "id": "7b478a27",
  "parentId": null,
  "timestamp": "2026-04-03T03:23:18.053797Z",
  "name": "default",
  "title": "Default Session"
}
```

### 3. `message` — 标准消息

**最重要的 entry type**，占 ~70% 的行数。

```json
{
  "type": "message",
  "message": {
    "role": "user | assistant | toolResult",
    "content": [
      {
        "type": "text",
        "text": "消息文本"
      },
      {
        "type": "thinking",
        "thinking": "内部思考内容"
      },
      {
        "type": "toolCall",
        "id": "call_d8c59e3849fb45649a4ea4b1",
        "name": "read",
        "arguments": {"path": "/some/file.go"}
      },
      {
        "type": "image",
        "data": "...",
        "media_type": "image/png"
      }
    ],
    "timestamp": 1775186637,
    "agent_visible": true,
    "user_visible": true
  }
}
```

**关键点**：
- 消息内容在 `entry.message.content`，**不是** `entry.content`
- `content` 是类型化对象数组，一个消息可以有多个 content block
- `role` 取值：`user`（用户输入）、`assistant`（LLM 回复）、`toolResult`（工具返回）
- `thinking` block 仅在 assistant 消息中出现
- `toolCall` block 仅在 assistant 消息中出现，id 用于关联 toolResult
- `toolResult` 消息的 content 通常是 `[{"type": "text", "text": "..."}]`
- `agent_visible` / `user_visible` 控制可见性

### 4. `truncate` — 截断事件

由 `truncate_messages` 工具产生，记录某个工具输出被截断。

```json
{
  "type": "truncate",
  "truncate": {
    "tool_call_id": "call_d8c59e3849fb45649a4ea4b1",
    "turn": 3,
    "trigger": "context_management",
    "timestamp": "2026-04-03T11:26:54+08:00"
  }
}
```

**分析价值**：
- `tool_call_id` 可回溯到被截断的具体工具调用
- `turn` 标识截断发生的轮次
- 高频截断可能意味着工具输出过大或触发阈值过于敏感

### 5. `compact` — 压缩事件

由 `compact_messages` 工具产生，包含完整的压缩摘要。

```json
{
  "type": "compact",
  "compact": {
    "summary": "## Current Task\n...\n## Files Involved\n...\n## Key Decisions\n...",
    "kept_message_count": 8,
    "turn": 6,
    "timestamp": "2026-04-03T11:35:59+08:00"
  }
}
```

**分析价值**：
- `summary` 是 agent 保留的完整上下文摘要，可评估信息保留质量
- `kept_message_count` 显示压缩后保留的消息数
- 压缩前后对比：compact 后 agent 是否丢失了关键信息？

## 辅助文件

### meta.json

```json
{
  "id": "0f04fb49-a956-4bee-8cd6-595896fcd225",
  "name": "default",
  "title": "Default Session",
  "createdAt": "2026-04-03T11:23:18.058479+08:00",
  "updatedAt": "2026-04-03T11:24:29.467318+08:00",
  "messageCount": 0
}
```

注意：`messageCount` 可能不准确，实际消息数应从 messages.jsonl 统计。

### checkpoint_index.json

```json
{
  "latest_checkpoint_turn": 8,
  "latest_checkpoint_path": "checkpoints/checkpoint_00003",
  "checkpoints": [
    {
      "turn": 0,
      "message_index": 0,
      "path": "checkpoints/checkpoint_00000",
      "created_at": "2026-04-03T11:23:18+08:00"
    },
    {
      "turn": 4,
      "message_index": 84,
      "path": "checkpoints/checkpoint_00001",
      "created_at": "2026-04-03T11:29:06+08:00",
      "llm_context_chars": 2756,
      "recent_messages_count": 61
    }
  ]
}
```

### agent_state.json（在每个 checkpoint 内）

```json
{
  "WorkspaceRoot": "~/.ai/sessions/--...--/<session-id>",
  "TotalTurns": 8,
  "TokensUsed": 38136,
  "TokensLimit": 200000,
  "LastLLMContextUpdate": 8,
  "LastCheckpoint": 6,
  "LastTriggerTurn": 7,
  "TurnsSinceLastTrigger": 1,
  "ToolCallsSinceLastTrigger": 30,
  "ActiveToolCalls": [],
  "SessionID": "sess_...",
  "CreatedAt": "2026-04-03T11:23:18+08:00",
  "UpdatedAt": "2026-04-03T11:35:59+08:00"
}
```

**触发追踪字段**：
- `LastTriggerTurn`：上次触发 context management 的 turn
- `TurnsSinceLastTrigger`：距上次触发的 turn 数
- `ToolCallsSinceLastTrigger`：距上次触发的工具调用数

### llm_context.txt（在每个 checkpoint 内）

Markdown 格式，是 agent 在该 checkpoint 时刻的记忆快照。内容包括任务状态、关键决策、已知信息等。可用于评估 agent 的信息保留质量。

## 常用读取命令

### 会话统计

```bash
# Entry type 分布
python3 -c "
import json
from collections import Counter
types = Counter()
with open('<path>/messages.jsonl') as f:
    for line in f:
        entry = json.loads(line)
        types[entry.get('type','?')] += 1
for t, c in types.most_common():
    print(f'{t}: {c}')
"
```

### 提取对话内容

```bash
# 用户和 assistant 的文本消息
python3 -c "
import json
with open('<path>/messages.jsonl') as f:
    for line in f:
        entry = json.loads(line)
        if entry.get('type') != 'message': continue
        msg = entry['message']
        role = msg['role']
        for block in msg.get('content', []):
            if block.get('type') == 'text':
                print(f'[{role}] {block[\"text\"][:200]}')
                print()
"
```

### 提取工具调用

```bash
python3 -c "
import json
with open('<path>/messages.jsonl') as f:
    for line in f:
        entry = json.loads(line)
        if entry.get('type') != 'message': continue
        msg = entry['message']
        for block in msg.get('content', []):
            if block.get('type') == 'toolCall':
                print(f'{block[\"name\"]}: {json.dumps(block.get(\"arguments\",{}), ensure_ascii=False)[:120]}')
"
```

### 提取截断和压缩事件

```bash
python3 -c "
import json
with open('<path>/messages.jsonl') as f:
    for i, line in enumerate(f):
        entry = json.loads(line)
        if entry.get('type') == 'truncate':
            t = entry['truncate']
            print(f'[TRUNCATE] line={i} turn={t[\"turn\"]} tool={t[\"tool_call_id\"][:20]}...')
        elif entry.get('type') == 'compact':
            c = entry['compact']
            print(f'[COMPACT] line={i} turn={c[\"turn\"]} kept={c[\"kept_message_count\"]} summary_len={len(c[\"summary\"])}')
"
```

## 报告框架（分析时使用）

在分析 session 时，按照以下框架总结：

1. **目标** — 用户意图（第一条用户消息）
2. **过程** — 关键步骤、工具使用、决策
3. **结果** — 是否成功？最终状态？
4. **问题** — 错误、重试、变通、浪费
5. **成本** — 总花费和 token 使用
6. **上下文管理** — 触发次数、截断/压缩效果、记忆质量（新增）

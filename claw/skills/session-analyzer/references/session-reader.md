# Session Format Reference

## Journal 文件：messages.jsonl

Append-only journal，每行一个 JSON entry。共 3 种 entry type：

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

**最重要的 entry type**，占绝大多数行数。

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

**注意**：截断和压缩事件不再作为独立 entry type 出现，而是通过 trace events 追踪：
- `context_mgmt_messages_truncated` — 消息截断（含 count、id 列表）
- `context_mgmt_llm_context_updated` — LLM context 更新
- `tool_output_truncated` — 工具输出截断
- `compaction` — 压缩操作

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
  "WorkspaceRoot": "/Users/genius/project/ai",
  "CurrentWorkingDir": "/Users/genius/project/ai",
  "TotalTurns": 19,
  "TokensUsed": 48391,
  "TokensLimit": 200000,
  "LastLLMContextUpdate": 175,
  "LastCheckpoint": 0,
  "LastTriggerTurn": 175,
  "TurnsSinceLastTrigger": 0,
  "ToolCallsSinceLastTrigger": 0,
  "TotalTruncations": 4,
  "TotalCompactions": 1,
  "LastCompactTurn": 114,
  "ActiveToolCalls": [],
  "RuntimeMetaTurns": 5,
  "RuntimeMetaSnapshot": "<agent:runtime_state .../>",
  "RuntimeMetaBand": "20-40",
  "SessionID": "",
  "CreatedAt": "2026-04-03T11:23:18+08:00",
  "UpdatedAt": "2026-04-03T11:35:59+08:00"
}
```

**触发追踪字段**：
- `LastTriggerTurn`：上次触发 context management 的 turn
- `TurnsSinceLastTrigger`：距上次触发的 turn 数
- `ToolCallsSinceLastTrigger`：距上次触发的工具调用数
- `TotalTruncations`：总截断次数
- `TotalCompactions`：总压缩次数
- `RuntimeMetaBand`：当前 token 使用区间（如 "20-40" 表示 20%-40%）

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

### 提取工具结果大小分布

```bash
python3 -c "
import json
from collections import Counter
sizes = []
with open('<path>/messages.jsonl') as f:
    for line in f:
        entry = json.loads(line)
        if entry.get('type') != 'message': continue
        msg = entry['message']
        if msg.get('role') != 'toolResult': continue
        for block in msg.get('content', []):
            if block.get('type') == 'text':
                sizes.append(len(block.get('text', '')))

buckets = Counter()
for s in sizes:
    if s == 0: buckets['0'] += 1
    elif s < 1000: buckets['<1K'] += 1
    elif s < 5000: buckets['1-5K'] += 1
    elif s < 9990: buckets['5-10K'] += 1
    else: buckets['~10K (truncated?)'] += 1

for b, c in sorted(buckets.items()):
    print(f'{b}: {c}')
"
```

## 报告框架（分析时使用）

在分析 session 时，按照以下框架总结：

1. **目标** — 用户意图（第一条用户消息）
2. **过程** — 关键步骤、工具使用、决策
3. **结果** — 是否成功？最终状态？
4. **问题** — 错误、重试、变通、浪费
5. **成本** — 总花费和 token 使用
6. **上下文管理** — 触发次数、截断/压缩效果、记忆质量
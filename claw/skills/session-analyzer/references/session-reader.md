# Session Format Reference

## 双 Schema 体系

`messages.jsonl` 中的 entry 存在两套 schema，由不同包写入：

| Schema | 包 | Entry Types | 写入方式 |
|--------|-----|------------|---------|
| **SessionEntry** | `pkg/session` | `session`, `session_info`, `message`, `compaction`, `branch_summary` | Session API（AppendMessage, Compact 等） |
| **JournalEntry** | `pkg/context` | `message`, `truncate`, `compact` | Journal API（AppendTruncate, AppendCompact 等） |

**共存原因**：旧 session 使用 JournalEntry 格式，新 session 使用 SessionEntry 格式。实际数据中两种格式共存。

**加载行为**：`pkg/session` 的 `decodeSessionEntry` 会跳过没有 `id` 字段的 entry（如 `truncate` 和旧 `compact`），这些由 `pkg/context` 的 reconstruction 逻辑单独处理。

### 实际数据中的 entry type 分布

```
message: 115238    truncate: 2354    session: 1667    session_info: 1404
compact: 44        compaction: 35
```

## SessionEntry 格式（新）

### 1. `session` — 头部标记

仅出现一次，在文件开头。

```json
{
  "type": "session",
  "version": 1,
  "id": "0f04fb49-a956-4bee-8cd6-595896fcd225",
  "timestamp": "2026-04-03T03:23:18.053792Z",
  "cwd": "/Users/genius/project/ai",
  "parentSession": "",
  "lastCompactionId": "e1d57256",
  "resumeOffset": 0
}
```

**新增字段**：
- `parentSession`：父会话路径（用于 fork session，空字符串表示无父会话）
- `lastCompactionId`：最近一次 compaction entry 的 ID（用于快速 resume）
- `resumeOffset`：文件偏移量（用于快速 resume）

### 2. `session_info` — 会话信息

通常出现一次，在 session 之后。

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

**最常见的 entry type**。

```json
{
  "type": "message",
  "id": "a1b2c3d4",
  "parentId": "7b478a27",
  "timestamp": "2026-04-03T03:23:19.123456Z",
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
- `id` 是 8 字符短 ID，`parentId` 指向前一个 entry（链式结构）

### 4. `compaction` — 压缩事件（新格式）

由 `Session.Compact()` 产生，记录会话压缩操作。

```json
{
  "type": "compaction",
  "id": "e1d57256",
  "parentId": "13f989ea",
  "timestamp": "2026-04-08T02:25:19.367459Z",
  "summaryFile": "llm-context/summaries/compact-20260408-102519.md",
  "firstKeptEntryId": "1a4f57a7",
  "tokensBefore": 142018
}
```

或（inline summary fallback）：

```json
{
  "type": "compaction",
  "id": "53b59907",
  "parentId": "6e6d3efa",
  "timestamp": "2026-04-08T09:47:59.37045Z",
  "summary": "## Current Task\n...",
  "firstKeptEntryId": "9d0d9128",
  "tokensBefore": 66281
}
```

**字段说明**：
- `summaryFile`：compaction summary 文件的相对或绝对路径（优先使用）
- `summary`：内联 summary 文本（fallback，当文件写入失败时使用）
- `firstKeptEntryId`：压缩后保留的第一条 entry 的 ID
- `tokensBefore`：压缩前的 token 数

**summary 存储位置**（随版本演变）：
- 新格式：`llm-context/summaries/compact-YYYYMMDD-HHMMSS.md`
- 旧格式：`llm-context/detail/compaction-YYYY-MM-DD-HHMMSS.md`
- 部分 entry 使用绝对路径引用

### 5. `branch_summary` — 分支摘要

用于 fork session 时记录源分支的摘要信息。代码中已定义但实际数据中尚未观测到。

## JournalEntry 格式（旧）

### `truncate` — 截断事件

由 context management 写入，记录工具输出被截断。

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

**注意**：此 entry 没有 `id` 字段，由 `pkg/context` 写入，`pkg/session` 加载时会跳过。

### `compact` — 压缩事件（旧格式）

旧版压缩格式，新 session 使用 `compaction` 代替。

```json
{
  "type": "compact",
  "compact": {
    "summary": "## Current Task\n...\n## Files Involved\n...",
    "kept_message_count": 8,
    "turn": 6,
    "timestamp": "2026-04-03T11:35:59+08:00"
  }
}
```

**注意**：此 entry 没有 `id` 字段，`pkg/session` 加载时会跳过。

## 辅助文件

### meta.json

```json
{
  "id": "6cc69bb2-d762-4a0d-95e7-4808d795005a",
  "name": "20260330-134926",
  "title": "20260330-134926",
  "createdAt": "2026-03-30T13:49:26.937023+08:00",
  "updatedAt": "2026-03-30T13:51:47.002062+08:00",
  "messageCount": 18,
  "workspace": "/Users/genius/project/ai",
  "currentWorkdir": "/Users/genius/project/ai"
}
```

**注意**：
- `messageCount` 可能不准确，实际消息数应从 messages.jsonl 统计
- `workspace` 和 `currentWorkdir` 为可选字段

### status.json

运行时状态文件，记录会话进程信息。

```json
{
  "session_id": "6cc69bb2-d762-4a0d-95e7-4808d795005a",
  "pid": 94736,
  "status": "completed",
  "current_turn": 7,
  "last_tool": "task_tracking",
  "last_activity": "2026-03-30T13:51:46.996838+08:00",
  "started_at": "2026-03-30T13:49:26.937508+08:00"
}
```

**分析价值**：
- `status`：`running` / `completed` / `crashed` — 判断会话是否正常结束
- `current_turn` / `last_tool`：快速了解会话进展
- `last_activity`：判断会话是否仍在活跃

### llm-context/overview.md

Markdown 格式，是 agent 的外部记忆文件。每次请求时内容会被加载到 prompt 中。

模板结构：
```
## 当前任务
## 关键决策
## 已知信息
## 待解决
## 最近操作
```

### llm-context/summaries/

存放 compaction summary 文件（`compact-YYYYMMDD-HHMMSS.md`）。由 `LLMContext.SaveCompactionSummary()` 创建。

### llm-context/detail/

存放详细内容文件，包括：
- compaction summary（旧格式：`compaction-YYYY-MM-DD-HHMMSS.md`）
- pre-compact 备份（`pre-compact-YYYYMMDD-HHMMSS.jsonl`）

## 常用读取命令

### 会话统计（兼容双 schema）

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
# 用户和 assistant 的文本消息（兼容新旧格式）
python3 -c "
import json
with open('<path>/messages.jsonl') as f:
    for line in f:
        entry = json.loads(line)
        if entry.get('type') != 'message': continue
        msg = entry.get('message')
        if msg is None: continue
        role = msg.get('role', '?')
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
        msg = entry.get('message')
        if msg is None: continue
        for block in msg.get('content', []):
            if block.get('type') == 'toolCall':
                print(f'{block[\"name\"]}: {json.dumps(block.get(\"arguments\",{}), ensure_ascii=False)[:120]}')
"
```

### 提取截断和压缩事件（兼容新旧格式）

```bash
python3 -c "
import json
with open('<path>/messages.jsonl') as f:
    for i, line in enumerate(f):
        entry = json.loads(line)
        t = entry.get('type')
        if t == 'truncate':
            tr = entry['truncate']
            print(f'[TRUNCATE] line={i} turn={tr[\"turn\"]} tool={tr[\"tool_call_id\"][:20]}...')
        elif t == 'compact':
            c = entry['compact']
            print(f'[COMPACT] line={i} turn={c[\"turn\"]} kept={c[\"kept_message_count\"]} summary_len={len(c[\"summary\"])}')
        elif t == 'compaction':
            sf = entry.get('summaryFile', '')
            sl = len(entry.get('summary', ''))
            print(f'[COMPACTION] line={i} id={entry.get(\"id\")} file={sf[:50] if sf else \"(inline)\"} summary_len={sl} tokens_before={entry.get(\"tokensBefore\",0)}')
"
```

### 读取 compaction summary 内容

```bash
# 从 compaction entry 的 summaryFile 字段读取 summary
python3 -c "
import json, os
session_dir = '<session-dir>'
with open(os.path.join(session_dir, 'messages.jsonl')) as f:
    for line in f:
        entry = json.loads(line)
        if entry.get('type') == 'compaction':
            sf = entry.get('summaryFile', '')
            if sf and not sf.startswith('/'):
                sf = os.path.join(session_dir, sf)
            if sf and os.path.exists(sf):
                with open(sf) as fh:
                    print(fh.read()[:500])
            elif entry.get('summary'):
                print(entry['summary'][:500])
            break
"
```

## 报告框架（分析时使用）

在分析 session 时，按照以下框架总结：

1. **目标** — 用户意图（第一条用户消息）
2. **过程** — 关键步骤、工具使用、决策
3. **结果** — 是否成功？最终状态？
4. **问题** — 错误、重试、变通、浪费
5. **上下文管理** — compaction/truncate 效果、llm_context 更新质量
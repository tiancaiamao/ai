# Session Analyzer - AI Code Review

像 senior engineer review 代码一样，分析 AI agent 的运行会话，发现设计问题并提供改进建议。

## 何时使用

- 周期性分析最近的 AI 对话（通过 /cron 触发）
- 发现 prompt 设计问题
- 识别工具选择错误
- 发现流程设计缺陷
- 找出反模式和可优化点
- 分析上下文管理效率（compaction 质量、truncate 行为、记忆保留）

**目标**：持续优化 ai 项目的代码质量

## 核心思路：Code Review 范式

**不是**技术指标统计（重试率、token 消耗）
**而是**设计洞察（为什么这里选错了工具？为什么这个 prompt 效果不好？compaction 后 agent 是否丢失了关键上下文？）

### 分析视角

1. **Prompt 设计**：系统提示词、工具描述是否清晰
2. **工具选择**：agent 是否选对了工具，为什么选错
3. **流程设计**：任务分解是否合理，执行顺序是否最优
4. **错误处理**：失败时的重试、fallback 策略
5. **反模式**：重复失败、无效循环、过度调用
6. **Subagent 协作**：delegation 是否合理，任务描述是否清晰
7. **上下文管理**：compaction 保留质量、truncate 行为、llm_context 更新效果

## 数据源

**⚠️ 重要：路径必须正确**

- **Sessions**: `~/.ai/sessions/--<cwd>--/<session-id>/messages.jsonl`
- **Traces**: `~/.ai/traces/pid<pid>-sess<session-id>.<N>.perfetto.json`
  - N 是每次 prompt 递增的序号（1, 2, 3...）
  - 大文件可能还有 `-part-X` 后缀

**不是** `~/.aiclaw/` 目录！

### Session 目录结构（当前架构）

```
~/.ai/sessions/--<cwd>--/<session-id>/
├── meta.json                    # 会话元数据 (id, name, title, createdAt, updatedAt, messageCount)
├── messages.jsonl               # Append-only journal（双 schema 共存，见 session-reader.md）
├── messages.jsonl.lock          # 文件锁
├── status.json                  # 运行时状态 (session_id, pid, status, current_turn, last_tool)
└── llm-context/
    ├── overview.md              # 外部记忆文件（每次请求时加载到 prompt）
    ├── summaries/               # compaction summary 文件 (compact-YYYYMMDD-HHMMSS.md)
    └── detail/                  # 详细内容目录（旧 compaction summary、pre-compact 备份）
```

**关于 checkpoint**：`pkg/context/checkpoint.go` 中有完整的 checkpoint 实现（`checkpoints/checkpoint_NNNNN/` + `checkpoint_index.json`），但大多数 session 在实际运行中**不产生** checkpoint 目录。不要假设 checkpoint 文件存在，使用前先检查。

### 辅助数据文件

| 文件 | 用途 | 分析价值 |
|------|------|----------|
| `status.json` | 运行时状态 | 判断会话正常结束(`completed`)还是异常(`crashed`/`running`) |
| `llm-context/overview.md` | 外部记忆 | 评估 agent 的信息保留质量 |
| `llm-context/summaries/` | Compaction summary | 查看压缩时保留/丢弃了哪些信息 |
| `meta.json` | 会话元数据 | 快速获取创建时间、消息计数 |

### Entry Type 速查

`messages.jsonl` 中存在双 schema（详见 `references/session-reader.md`）：

| Entry Type | Schema | 来源包 | 说明 |
|-----------|--------|--------|------|
| `session` | SessionEntry | pkg/session | 头部标记，仅一次 |
| `session_info` | SessionEntry | pkg/session | 会话名称/标题 |
| `message` | SessionEntry | pkg/session | 用户/assistant/toolResult 消息 |
| `compaction` | SessionEntry | pkg/session | **新**压缩事件（summaryFile/summary + firstKeptEntryId） |
| `branch_summary` | SessionEntry | pkg/session | fork session 时的分支摘要 |
| `truncate` | JournalEntry | pkg/context | 工具输出截断记录 |
| `compact` | JournalEntry | pkg/context | **旧**压缩事件（inline summary + kept_message_count） |

## 工作流程

### 步骤 0：快速会话概览

用 python3 内联命令快速了解会话结构和统计：

```bash
# 会话 entry type 分布（兼容双 schema）
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

# 查看会话元数据
cat <path>/meta.json | python3 -m json.tool

# 查看运行时状态（是否正常结束）
cat <path>/status.json | python3 -m json.tool

# 查看外部记忆
cat <path>/llm-context/overview.md

# 列出 compaction summary 文件（如果有）
ls -la <path>/llm-context/summaries/ 2>/dev/null || echo "No summaries dir"

# 检查 checkpoint 是否存在（不保证）
cat <path>/checkpoint_index.json 2>/dev/null | python3 -m json.tool || echo "No checkpoint index"
```

### 步骤 1：选择分析模式

根据分析目标选择模式：

| 模式 | 目标 | 适用场景 |
|------|------|----------|
| `full` | 全面分析 | 周期性深度 review |
| `tools` | 仅工具选择 | 快速发现工具使用问题 |
| `flow` | 仅流程设计 | 优化执行效率 |
| `prompt` | 仅 prompt 设计 | 调试理解问题 |
| `subagents` | 仅 subagent | 优化 delegation 策略 |
| `context-mgmt` | 上下文管理行为 | 分析 compaction/truncate/llm_context 效率 |

### 步骤 2：选择多个 session（批处理）

```bash
# 找到最近的 session（按修改时间）
ls -lt ~/.ai/sessions/--Users-genius-project-ai--/ | head -10

# 或按 messages.jsonl 大小排序（大 session 更可能有问题）
ls -lS ~/.ai/sessions/--Users-genius-project-ai--/*/messages.jsonl | head -10
```

**批处理策略**：
- **每次分析 5-10 个 session**
- 从 checkpoint 继续，避免重复分析
- 分析完所有 session 后，生成汇总报告

### 步骤 3：按模式逐个分析 session

#### 模式 A: 工具选择分析 (`tools`)

```bash
# 提取所有工具调用
python3 -c "
import json
with open('<path>/messages.jsonl') as f:
    for line in f:
        entry = json.loads(line)
        if entry.get('type') != 'message': continue
        msg = entry.get('message')
        if msg is None: continue
        if msg.get('role') != 'assistant': continue
        for block in msg.get('content', []):
            if block.get('type') == 'toolCall':
                print(f'{block[\"name\"]}: {json.dumps(block.get(\"arguments\",{}), ensure_ascii=False)[:120]}')
"
```

**关注点**：
- 是否选错了工具（如用 bash cat 代替 read）
- 参数是否合理（如 grep 范围过大）
- 调用频率是否异常（重复调用同一工具）
- 工具链是否可以简化

#### 模式 B: 流程设计分析 (`flow`)

```bash
# 提取任务流程（用户消息 + 工具调用序列）
python3 -c "
import json
with open('<path>/messages.jsonl') as f:
    for line in f:
        entry = json.loads(line)
        if entry.get('type') != 'message': continue
        msg = entry.get('message')
        if msg is None: continue
        role = msg.get('role', '?')
        if role == 'user':
            for block in msg.get('content', []):
                if block.get('type') == 'text':
                    print(f'\n[USER] {block[\"text\"][:200]}')
        elif role == 'assistant':
            tools = [b for b in msg.get('content', []) if b.get('type') == 'toolCall']
            if tools:
                for t in tools:
                    print(f'  -> {t[\"name\"]}')
            else:
                texts = [b for b in msg.get('content', []) if b.get('type') == 'text']
                if texts:
                    print(f'  [REPLY] {texts[0][\"text\"][:100]}')
"
```

**关注点**：
- 任务分解是否合理
- 执行顺序是否最优（是否有多余步骤）
- 是否有无效循环（反复尝试同一失败操作）
- 错误恢复路径是否合理

#### 模式 C: Prompt 设计分析 (`prompt`)

**关注点**：
- 系统提示词是否导致误解
- 工具描述是否清晰（是否导致选错工具）
- 上下文中是否有矛盾信息
- 是否有信息遗漏导致错误决策

#### 模式 D: Subagent 分析 (`subagents`)

```bash
# 提取 subagent 相关调用
python3 -c "
import json
with open('<path>/messages.jsonl') as f:
    for line in f:
        entry = json.loads(line)
        if entry.get('type') != 'message': continue
        msg = entry.get('message')
        if msg is None: continue
        for block in msg.get('content', []):
            if block.get('type') == 'toolCall' and block['name'] in ('subagent', 'spawn_subagent'):
                args = block.get('arguments', {})
                print(f'[SUBAGENT] task: {args.get(\"task\", args.get(\"prompt\", \"?\"))[:100]}')
"
```

**关注点**：
- 任务描述是否清晰（agent 是否理解了 delegation）
- 子任务是否过度执行（是否做了不必要的工作）
- 结果是否被正确利用（delegation 结果是否被整合）
- 是否应该 delegation 但没有（大任务是否应该拆分）

#### 模式 E: 上下文管理分析 (`context-mgmt`)

分析 context 管理效率，包括 compaction、truncate、llm_context 更新。

**核心问题**：agent 在长会话中是否有效管理了上下文窗口？

```bash
# 提取所有 compaction 事件（兼容新旧格式）
python3 -c "
import json
with open('<path>/messages.jsonl') as f:
    for i, line in enumerate(f):
        entry = json.loads(line)
        t = entry.get('type')
        if t == 'compact':
            c = entry['compact']
            print(f'[COMPACT] line={i} turn={c[\"turn\"]} kept={c[\"kept_message_count\"]} summary_len={len(c[\"summary\"])}')
        elif t == 'compaction':
            sf = entry.get('summaryFile', '')
            sl = len(entry.get('summary', ''))
            print(f'[COMPACTION] line={i} id={entry.get(\"id\")} file={sf[:50] if sf else \"(inline)\"} summary_len={sl} tokens_before={entry.get(\"tokensBefore\",0)}')
"

# 提取所有 truncate 事件
python3 -c "
import json
with open('<path>/messages.jsonl') as f:
    for i, line in enumerate(f):
        entry = json.loads(line)
        if entry.get('type') == 'truncate':
            tr = entry['truncate']
            print(f'[TRUNCATE] line={i} turn={tr[\"turn\"]} tool_call={tr[\"tool_call_id\"][:20]}...')
"

# 查看 llm_context overview 的演变
cat <path>/llm-context/overview.md

# 读取 compaction summary 内容（从 summaryFile 引用）
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
                    print(fh.read()[:800])
            elif entry.get('summary'):
                print(entry['summary'][:800])
            break
"
```

**分析维度**：

| 信号 | 好的信号 | 坏的信号 |
|------|----------|----------|
| `compaction` | Summary 准确保留关键决策和上下文 | Summary 遗漏重要信息、压缩后 agent 行为退化 |
| `truncate` | 截断的是低价值/大体积输出 | 截断了有用内容、频繁截断暗示工具输出控制不足 |
| `llm_context` 更新 | 保留了关键决策和当前任务状态 | 内容过时、格式混乱、遗漏重要变更 |
| `summaryFile` 引用 | 文件存在且内容完整 | 文件路径无效或内容为空 |

#### 模式 F: Traces 性能分析（可选）

当需要深入理解特定行为时，配合 trace 文件：

```bash
# Context management 相关 trace events（当前有效的事件名）
python3 -c "
import json
target_events = ['context_update_reminder', 'context_decision_reminder', 'context_mgmt_messages_truncated', 'context_mgmt_llm_context_updated']
with open('<trace-path>') as f:
    data = json.load(f)
for e in data.get('traceEvents', []):
    if e.get('name') in target_events:
        print(f'{e[\"name\"]}: {json.dumps(e.get(\"args\",{}), ensure_ascii=False)[:200]}')
"
```

**当前有效的 context management trace events**（定义在 `pkg/traceevent/config.go`）：
- `context_update_reminder` — Context 更新提醒
- `context_decision_reminder` — Context 决策提醒
- `context_mgmt_messages_truncated` — 消息截断操作
- `context_mgmt_llm_context_updated` — LLM context 文件更新

其他常用 trace events：
- `prompt` — Prompt 构建
- `llm_call` — LLM API 调用
- `tool_execute` — 工具执行
- `mini_compact` / `mini_compact_check` — Mini compaction 相关

---

## 报告格式（Code Review Style）

```markdown
# Session Analysis - Code Review Style

**Session**: <session-id>
**时间**: <timestamp>
**工作目录**: <cwd>
**分析模式**: <tools/flow/prompt/subagents/context-mgmt>

## 摘要
- 用户意图：<一句话描述>
- agent 执行：<一句话描述结果>
- 发现问题：<数量> 个

---

## 🔴 Critical Issues

### Issue 1: <具体问题>

**位置**：messages.jsonl:23（第 23 轮对话）

**问题描述**：
agent 选择了 bash cat 而不是 read 工具读取文件

**具体证据**：
\```
User: "读取 pkg/agent/loop.go 文件"
Agent: 调用 bash 工具，执行 `cat pkg/agent/loop.go`
\```

**为什么这是个问题**：
- read 工具有路径补全、错误处理更好
- bash cat 可能受 shell 转义影响
- 违反了"优先使用专用工具"原则

**根因分析**：
- tools.md 中 read 工具描述不够清晰
- 没有明确说明"读取文件用 read，执行命令用 bash"

**改进建议**：
\```go
// 在 tools.md 中添加：
- read: 读取文件内容（支持路径补全、自动错误处理）
- bash: 执行 shell 命令（仅当需要 shell 特性时使用）
\```

---

## 🟡 Medium Issues

### Issue N: <具体问题>

（同上格式，包含位置、证据、根因、建议）

---

## 🟢 Suggestions

### Suggestion N: <优化建议>

（同上格式，包含观察、优化方案、收益）

---

## 总结

### 优先修复
1. [Critical] ...

### 可选优化
1. [Suggestion] ...

### 下次关注点
- 检查修复后的效果
- 关注是否有新的反模式
- 监控上下文管理效率
```

## 分析重点

### ✅ 应该关注的

1. **设计问题**：prompt 不清晰、工具描述有歧义
2. **选择错误**：选错工具、执行顺序不合理
3. **反模式**：重复失败、无效循环、过度调用
4. **Subagent 协作**：任务描述不清、过度执行、结果不完整
5. **上下文管理效率**：compaction 是否保留关键信息、truncate 是否合理、llm_context 更新质量
6. **改进机会**：可以优化的流程、可以复用的模式

### ❌ 不应该关注的

1. **技术指标**：重试率、token 消耗（用脚本统计即可）
2. **性能数据**：P50/P95/P99 响应时间（监控工具负责）
3. **数量统计**：文件数、调用次数（不提供洞察）

## 注意事项

- **慢工出细活**：这是离线分析，要像 code review 一样深入思考
- **⚠️ 批处理策略**：每次分析 **5-10 个 session**，然后生成汇总报告
  - 不要只分析 1 个（样本太少，看不出共性问题）
  - 不要一次分析太多（避免超时和 rate limit）
  - 逐个分析，中间等待 10-30 秒避免 rate limit
- **必须包含证据**：每个问题都要引用具体对话内容
- **可操作建议**：改进建议要具体到代码/配置修改
- **Code Review 范式**：像 senior engineer review PR 一样思考
- **目标明确**：持续优化 ai 项目的代码质量
- **汇总报告**：分析完一批 session 后，必须生成汇总报告（跨 session 共性问题）
- **双 schema 兼容**：分析脚本必须同时处理 `compact`/`compaction` 和 `truncate` 格式
- **不假设文件存在**：checkpoint_index.json、summaries/ 目录不保证存在

## 参考文档

- **Session Reader**: `references/session-reader.md` — 会话格式详细说明（双 schema、字段定义、读取命令）
- **Analyst**: `references/analyst.md` — 分析角色定义
- **Subagent**: `/skills/subagent` — 子代理机制和最佳实践
# Session Analyzer - AI Code Review

像 senior engineer review 代码一样，分析 AI agent 的运行会话，发现设计问题并提供改进建议。

## 何时使用

- 周期性分析最近的 AI 对话（通过 /cron 触发）
- 发现 prompt 设计问题
- 识别工具选择错误
- 发现流程设计缺陷
- 找出反模式和可优化点
- 分析上下文管理效率（触发频率、截断/压缩质量、记忆保留）

**目标**：持续优化 ai 项目的代码质量

## 核心思路：Code Review 范式

**不是**技术指标统计（重试率、token 消耗）
**而是**设计洞察（为什么这里选错了工具？为什么这个 prompt 效果不好？上下文管理是否高效？）

### 分析视角

1. **Prompt 设计**：系统提示词、工具描述是否清晰
2. **工具选择**：agent 是否选对了工具，为什么选错
3. **流程设计**：任务分解是否合理，执行顺序是否最优
4. **错误处理**：失败时的重试、fallback 策略
5. **反模式**：重复失败、无效循环、过度调用
6. **Subagent 协作**：delegation 是否合理，任务描述是否清晰
7. **上下文管理**：触发时机、截断/压缩质量、记忆保留效果

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
├── meta.json                    # 会话元数据 (id, name, title, createdAt, updatedAt)
├── messages.jsonl               # Append-only journal（唯一数据源，5 种 entry type）
├── messages.jsonl.lock          # 文件锁
├── checkpoint_index.json        # 所有 checkpoint 元信息索引
├── current -> checkpoints/checkpoint_NNNNN/  # 当前 checkpoint 符号链接
├── checkpoints/
│   ├── checkpoint_00000/
│   │   ├── llm_context.txt      # LLM 维护的结构化上下文（markdown）
│   │   └── agent_state.json     # 系统维护的元数据
│   ├── checkpoint_00001/
│   └── ...
└── llm-context/
    ├── overview.md              # 外部记忆文件
    └── detail/                  # 详细内容目录
```

### 辅助数据文件

| 文件 | 用途 | 分析价值 |
|------|------|----------|
| `checkpoint_index.json` | 所有 checkpoint 索引 | 追踪 checkpoint 创建频率、上下文增长趋势 |
| `agent_state.json` | 当前 agent 状态 | 查看触发追踪字段、token 使用量 |
| `llm_context.txt` | LLM 记忆快照 | 评估 agent 的信息保留质量 |

## 工作流程

### 步骤 0：快速会话概览

用 python3 内联命令快速了解会话结构和统计：

```bash
# 会话入口统计（entry type 分布）
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

# 查看 checkpoint 索引（上下文管理历史）
cat <path>/checkpoint_index.json | python3 -m json.tool

# 当前 agent 状态（token 使用、触发追踪）
cat <path>/checkpoints/$(readlink <path>/current | xargs basename)/agent_state.json | python3 -m json.tool
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
| `context-mgmt` | 上下文管理行为 | **新增**：分析 context 管理效率 |

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
        msg = entry['message']
        if msg['role'] != 'assistant': continue
        for block in msg.get('content', []):
            if block.get('type') == 'toolCall':
                print(f'Turn: {msg.get(\"timestamp\",\"?\")} Tool: {block[\"name\"]} Args: {json.dumps(block.get(\"arguments\",{}), ensure_ascii=False)[:100]}')
"
```

**关注点**：
- Agent 选择了什么工具？
- 是否选错了？（如应该用 read 却用了 bash cat）
- 为什么选错？工具描述不够清楚？
- 是否有更好的工具组合？

#### 模式 B: 流程设计分析 (`flow`)

```bash
# 读取对话流
tail -200 "<path>/messages.jsonl"
```

**关注点**：
- 任务分解是否合理？
- 是否有冗余步骤？
- 执行顺序是否最优？
- 是否可以并行执行？

#### 模式 C: Prompt 设计分析 (`prompt`)

**关注点**：
- Agent 是否正确理解了用户意图？
- 如果误解：系统提示词/工具描述是否不够清晰？
- 如果理解正确但执行错误：工具描述是否有歧义？

#### 模式 D: Subagent 分析 (`subagents`)

```bash
# 1. 从主 session 中找到 subagent 调用
grep "subagent-" ~/.ai/sessions/--<cwd>--/<session-id>/messages.jsonl

# 2. 通过 tmux session 找到对应的 AI session
tmux capture-pane -t subagent-<timestamp>-<random> -p -S - | grep "Session ID:"

# 3. 读取 subagent 的 messages.jsonl
cat ~/.ai/sessions/--<cwd>--/<subagent-session-id>/messages.jsonl
```

**关注点**：
- **任务描述清晰度**：父代理给子代理的指令是否明确？
- **子代理理解**：子代理是否理解了任务？
- **执行偏差**：子代理是否过度拆解/过度执行？
- **结果完整性**：子代理返回的结果是否完整？
- **协作效率**：父子代理间的交互是否合理？

#### 模式 E: 上下文管理分析 (`context-mgmt`) — **新增**

**这是新架构的核心分析维度**。关注 agent 如何管理自身上下文。

##### E1: 触发分析

```bash
# 截断事件统计
python3 -c "
import json
truncates = []
with open('<path>/messages.jsonl') as f:
    for i, line in enumerate(f):
        entry = json.loads(line)
        if entry.get('type') == 'truncate':
            truncates.append((i, entry['truncate']))
for idx, t in truncates:
    print(f'Line {idx}: turn={t[\"turn\"]} tool_call_id={t[\"tool_call_id\"][:20]}... trigger={t[\"trigger\"]}')
"

# 压缩事件
python3 -c "
import json
with open('<path>/messages.jsonl') as f:
    for i, line in enumerate(f):
        entry = json.loads(line)
        if entry.get('type') == 'compact':
            c = entry['compact']
            print(f'Line {i}: turn={c[\"turn\"]} kept={c[\"kept_message_count\"]} summary_len={len(c[\"summary\"])}')
            print(f'  Summary preview: {c[\"summary\"][:200]}...')
"
```

**关注点**：
- **触发频率**：多久触发一次 context management？是否过于频繁/稀少？
- **触发原因**：token 压力（70%/50%/30%/20%）vs stale output（≥15）vs 周期性
- **截断选择**：被截断的是哪些工具输出？截断是否合理？是否截掉了重要内容？
- **压缩质量**：compact summary 是否准确？压缩后 agent 是否丢失了关键上下文？

##### E2: Checkpoint 分析

```bash
# checkpoint 演变历史
python3 -c "
import json
with open('<path>/checkpoint_index.json') as f:
    idx = json.load(f)
print(f'Total checkpoints: {len(idx[\"checkpoints\"])}')
for cp in idx['checkpoints']:
    ctx_chars = cp.get('llm_context_chars', '?')
    msg_count = cp.get('recent_messages_count', '?')
    print(f'  Turn {cp[\"turn\"]}: context={ctx_chars} chars, messages={msg_count}, path={cp[\"path\"]}')
"
```

**关注点**：
- **上下文增长趋势**：`llm_context_chars` 是否持续增长？是否合理？
- **消息数量波动**：压缩/截断后 `recent_messages_count` 是否明显减少？
- **Checkpoint 间隔**：创建频率是否合理？

##### E3: Agent 状态分析

```bash
# 当前 agent 状态
cat <path>/checkpoints/$(readlink <path>/current | xargs basename)/agent_state.json | python3 -m json.tool
```

关键字段：
- `TokensUsed` / `TokensLimit`：token 使用率
- `LastTriggerTurn` / `TurnsSinceLastTrigger`：触发间隔
- `ToolCallsSinceLastTrigger`：距上次触发的工具调用数
- `LastLLMContextUpdate`：上次 LLM context 更新的 turn

##### E4: 上下文管理工具调用分析

```bash
# 找到所有上下文管理工具调用（在 ModeContextMgmt 中执行）
python3 -c "
import json
mgmt_tools = {'update_llm_context', 'truncate_messages', 'compact_messages', 'no_action'}
with open('<path>/messages.jsonl') as f:
    for i, line in enumerate(f):
        entry = json.loads(line)
        if entry.get('type') != 'message': continue
        msg = entry['message']
        for block in msg.get('content', []):
            if block.get('type') == 'toolCall' and block['name'] in mgmt_tools:
                print(f'Line {i}: {block[\"name\"]} args={json.dumps(block.get(\"arguments\",{}), ensure_ascii=False)[:200]}')
"
```

**上下文管理工具分析标准**：

| 工具 | 好的信号 | 坏的信号 |
|------|----------|----------|
| `update_llm_context` | 保留了关键决策和上下文 | 遗漏了重要信息、格式混乱、内容过时 |
| `truncate_messages` | 截断的是低价值/大体积输出 | 截断了有用内容、截断后未缓解 token 压力 |
| `compact_messages` | Summary 准确且精炼 | Summary 遗漏关键信息、压缩后 agent 行为退化 |
| `no_action` | 确实不需要操作 | 频繁 no_action 暗示触发阈值不合理 |

#### 模式 F: Traces 性能分析（可选）

当需要深入理解特定行为时，配合 trace 文件：

```bash
# Context management 相关 trace events
python3 -c "
import json
target_events = ['context_trigger_checked', 'context_management_decision', 'context_snapshot_evaluated']
with open('<trace-path>') as f:
    data = json.load(f)
for e in data.get('traceEvents', []):
    if e.get('name') in target_events:
        print(f'{e[\"name\"]}: {json.dumps(e.get(\"args\",{}), ensure_ascii=False)[:200]}')
"
```

可用的 context management trace events：
- `context_snapshot_evaluated` — Snapshot 评估
- `context_trigger_checked` — 触发检查结果
- `context_checkpoint_created` / `context_checkpoint_loaded` — Checkpoint 生命周期
- `context_journal_entry_appended` — Journal 写入
- `context_management` — Context management 操作 span
- `context_management_decision` — 管理决策
- `context_management_skipped` — 跳过管理

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
5. **上下文管理效率**：触发是否合理、截断/压缩质量、记忆保留效果
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
- **上下文管理是新维度**：重点关注新架构下的 context management 行为，这是最大变化

## 参考文档

- **Session Reader**: `references/session-reader.md` — 会话格式和读取方法
- **Architecture**: `references/architecture.md` — 新架构参考（trampoline、trigger、checkpoint）
- **Analyst**: `references/analyst.md` — 分析角色定义
- **Subagent**: `/skills/subagent` — 子代理机制和最佳实践

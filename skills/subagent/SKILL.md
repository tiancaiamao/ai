---
name: subagent
description: 使用 ai serve/send/watch/kill 控制子 agent 的通用指引。所有需要子 agent 的技能都应参考此技能，而非重复定义 spawn/cleanup 流程。
---

# Subagent Operations Guide

通过 `ai` CLI 的 `serve`/`send`/`watch`/`kill` 控制子 agent，实现任务委派和并行执行。

**本技能是子 agent 操作的唯一权威参考。** 其他技能（pge、review、explore、worker-judge、debate 等）如需使用子 agent，应引用本技能，而非重复定义 spawn/watch/kill 流程。

## When to Use

- 需要将任务委派给独立 agent（探索、实现、审查、验证等）
- 需要并行执行多个独立子任务
- 需要隔离上下文（子 agent 不继承主 agent 的对话历史）

**不要用于：** 简单的 bash 命令（直接执行即可）

## ⚠️ Concurrency Limit

**主 agent + 子 agent 同时运行总数不得超过 3**（即最多 2 个子 agent 同时运行）。
LLM 提供商限流：并发稍高即触发 API rate limit，导致子 agent 卡住或失败。

## How to Get Your Run ID

你的 run ID 在 `<agent:runtime_state>` 的 `run_id` 字段中。例如：

```yaml
context_meta:
  run_id: abc123
  ...
```

如果 `run_id` 为空，说明你是独立运行的（非 `ai serve` 启动），此时不使用本技能的隔离机制。

**在 bash 命令中，用 `$RUN_ID` 引用自己的 run ID**（从 runtime_state 中读取后赋值）：

```bash
RUN_ID=<your run_id from runtime_state>
```

## Agent Lifecycle（完整生命周期）

每个子 agent 必须经历完整的生命周期，不得跳过任何阶段：

```
┌─────────┐     ┌──────────────┐     ┌─────────┐     ┌──────────┐
│  Spawn  │────►│Collect Reply │────►│  Multi  │────►│ Cleanup  │
│ (tmux)  │     │(send --wait) │     │ (send)  │     │(kill)    │
└─────────┘     └──────────────┘     └─────────┘     └──────────┘
```

### 阶段 1: Spawn

**⚠️ `ai serve` 是阻塞命令，必须用 tmux 后台运行。** 不能用 `&`（bash tool 的 pipe 会卡死）。

**💡 `--input` 建议：** 推荐在 spawn 时通过 `--input` 或 `--input-file` 传入任务指令。不带 `--input` spawn 空壳是支持的用法（如预热、条件分发、多轮交互），但务必在流程中安排好后续 `ai send`，避免遗忘导致空跑浪费 token。

**⚠️ `--role` 优先于 `--system-prompt`：** 大多数场景应使用 `--role coder`（默认值），`ai` 会自动加载对应的 system prompt。仅在需要高度定制化的 role（如 validator with specific checklist）时才用 `--system-prompt`。

```bash
# 从 runtime_state 获取自己的 run ID
RUN_ID=<your run_id from runtime_state>

# 生成唯一的 tmux session 名和 id-file 路径
TMUX_SESSION="agent-$RUN_ID-my-task"
ID_FILE="/tmp/agent-$RUN_ID-my-task.id"

# 清理可能存在的旧 tmux session 和 id 文件
tmux kill-session -t "$TMUX_SESSION" 2>/dev/null
rm -f "$ID_FILE"

# 启动子 agent
tmux new-session -d -s "$TMUX_SESSION" \
  "ai serve --role coder \
   --input 'Fix the bug in auth.go' \
   --name 'fix-auth' \
   --id-file $ID_FILE"

# 等待 id 文件生成（serve 启动后写入）
sleep 2
CHILD_ID=$(cat "$ID_FILE")

# ⚠️ 重要：记录子 agent ID 到自己的 subagent 文件（用于隔离和 cleanup）
echo "$CHILD_ID" >> ~/.ai/runs/$RUN_ID/subagent
```

**如果任务描述很长**（比如超过 shell ARG_MAX），用 `--input-file`：
```bash
TMUX_SESSION="agent-$RUN_ID-long-task"
ID_FILE="/tmp/agent-$RUN_ID-long-task.id"
tmux kill-session -t "$TMUX_SESSION" 2>/dev/null
rm -f "$ID_FILE"
echo "$LONG_TASK_PROMPT" > /tmp/task-prompt.txt
tmux new-session -d -s "$TMUX_SESSION" \
  "ai serve --role coder \
   --input-file /tmp/task-prompt.txt \
   --name 'long-task' \
   --id-file $ID_FILE"
sleep 2
CHILD_ID=$(cat "$ID_FILE")
echo "$CHILD_ID" >> ~/.ai/runs/$RUN_ID/subagent
```

**仅在使用 `--system-prompt` 传入 @file 时支持引用 skill 目录下的文件：**

```bash
TMUX_SESSION="agent-$RUN_ID-validator"
ID_FILE="/tmp/agent-$RUN_ID-validator.id"
tmux kill-session -t "$TMUX_SESSION" 2>/dev/null
rm -f "$ID_FILE"
tmux new-session -d -s "$TMUX_SESSION" \
  "ai serve --system-prompt '@$HOME/.ai/skills/debate/references/proposer-system.md' \
   --input 'Argue FOR: ...' \
   --name 'proposer' \
   --id-file $ID_FILE"
sleep 2
CHILD_ID=$(cat "$ID_FILE")
echo "$CHILD_ID" >> ~/.ai/runs/$RUN_ID/subagent
```

**为什么用 `--id-file` 而不是 `tmux capture-pane`：** `--id-file` 更可靠，不受 tmux pane 内容格式影响。`tmux capture-pane` 依赖第一行输出格式，有时不稳定。

**为什么要 `echo >> subagent`：** 这是子 agent 隔离的关键。写入 `~/.ai/runs/$RUN_ID/subagent` 文件后：
- Cleanup 时你知道该 kill 哪些子 agent
- 你**绝对不会误杀其他 agent**（因为只 kill 文件里记录的 ID）

**⚠️ tmux session 命名规则：** 必须包含 `$RUN_ID` 前缀（如 `agent-$RUN_ID-xxx`），避免与其他 agent 的 tmux session 冲突。

### 阶段 2: 收集回复

spawn 时通过 `--input` 传入任务后，子 agent 会立即开始处理。**根据预期输出长度选择收集方式**：

#### 方式 C（长输出任务）: 写文件 + 观察等待

> **适用场景：** review、代码探索、设计文档、任何预期输出 >500 字的任务。
>
> **原理：** spawn 时就告诉子 agent把结果写到文件。主 agent 用 `watch` 观察进度（不打断），完成后读文件。**零截断、零打断**。

```bash
# spawn 时在 --input 里指示写文件
RESULT_FILE="/tmp/task-$RUN_ID-result.md"

tmux new-session -d -s "$TMUX_SESSION" \
  "ai serve --role coder \
   --input '...Write your complete output to $RESULT_FILE...When done, output DONE.' \
   --name 'my-task' \
   --id-file $ID_FILE"

# 观察进度（不打断，不注入新 prompt）
ai watch --id "$CHILD_ID" --follow --pretty --timeout 15m

# 完成后读文件（完整内容，无截断）
cat "$RESULT_FILE"
```

**为什么长任务必须用方式 C：**
- 方式 A 的 `--summary` 会截断长输出（实测超 ~3K tokens 就截断）
- 方式 A 会注入新 prompt，打断正在干活（如深度代码核查）的子 agent
- 文件写入不受任何长度限制，主 agent 读到的是完整结果

#### 方式 A（短输出 / 多轮交互）: `ai send --wait`

> **适用场景：** 状态查询、简单问答、多轮对话追加反馈。

```bash
# ⚠️ ai send 必须带消息参数，即使是等待 --input 的回复
ai send --id "$CHILD_ID" --wait --summary --timeout 20m "请给出你的完整结果"
```

**⚠️ 方式 A 会注入一条新 prompt。** 子 agent 会把它当作追问处理。如果子 agent 正在执行长任务（如深度代码核查），这条注入会打断它——**长任务不要用方式 A**。

#### 方式 B（观察中间过程）: `ai watch --follow`

> **适用场景：** 需要实时看子 agent 的 tool calls / thinking 过程。

```bash
# ⚠️ 必须加 --pretty，否则非交互终端无输出
ai watch --id "$CHILD_ID" --follow --pretty --timeout 20m
```

> **方式 B 不注入 prompt、不打断**，适合"偷看"子 agent 在干什么。但中间 tool calls 会刷屏，长任务看起来像卡住了（其实在做大量沉默的 grep/read）。配合方式 C（写文件）一起用效果最好。

#### 决策表

| 预期输出 | 收集方式 | 观察方式 |
|---------|---------|---------|
| 短（问答、状态） | 方式 A (`send --wait --summary`) | 不需要 |
| 长（review、探索、设计） | 方式 C（写文件 + 读文件） | 方式 B (`watch --follow --pretty`) |
| 需要中间过程调试 | 方式 B | — |

**⚠️ `ai send` 必须带消息参数。** 即使子 agent 已经通过 `--input` 收到了任务，`ai send` 也不支持无消息调用（会报错 `error: no message provided`）。消息内容可以是简单的追问，比如 `"请给出你的分析结果"` 或 `"继续"`。

### 阶段 3: Multi-Turn（可选）

如果需要多轮交互（如追加反馈），继续用 `ai send --wait`：

```bash
ai send --id "$CHILD_ID" --wait 'Please also handle the error case where input is nil'
```

只需最终文本，不需要看中间 tool calls：

```bash
ai send --id "$CHILD_ID" --wait --summary 'Summarize what you found about the auth module'
```

### 阶段 4: Cleanup（⚠️ 必须执行）

**`ai serve` 是长驻进程，不会自动退出。** `agent_end` 只代表当前 prompt 处理完，不代表进程退出。watch 返回后，**必须**显式清理：

```bash
# 从 subagent 文件读取所有子 agent ID 并逐一 kill
RUN_ID=<your run_id from runtime_state>
while read -r CHILD_ID; do
  ai kill --id "$CHILD_ID" 2>/dev/null
done < ~/.ai/runs/$RUN_ID/subagent

# 清理 tmux sessions（只清理自己的）
# ⚠️ 只 kill session 名匹配 "agent-$RUN_ID-*" 的 tmux session
tmux list-sessions -F '#{session_name}' 2>/dev/null | grep "^agent-$RUN_ID-" | while read -r s; do
  tmux kill-session -t "$s" 2>/dev/null
done

# 清理 id 文件
rm -f /tmp/agent-$RUN_ID-*.id

# 清理 subagent 记录
rm -f ~/.ai/runs/$RUN_ID/subagent
```

**为什么不自动退出：** `ai serve` 的设计就是后台服务模式，可以接收多轮 `ai send`。退出应由调用方（即你）负责。这是 `ai serve` 的正确行为。

### 生命周期铁律

| 规则 | 违反后果 |
|------|---------|
| **每个 spawn 必须对应一个 kill** | 进程堆积，内存泄漏（每个 ~20MB） |
| **watch 返回后立即 kill** | 延迟 kill = 忘记 kill |
| **异常路径也要 kill** | 主 agent 崩溃会留下孤儿进程 |
| **spawn 后必须写入 subagent 文件** | 无法追踪子 agent，可能忘记 cleanup |
| **tmux session 名必须含 `$RUN_ID`** | 与其他 agent 的 session 冲突 |

## ⚠️ Kill Safety（防误杀）

**这是最重要的安全规则。违反会导致其他 agent 被杀。**

### ✅ Safe kill pattern

```bash
# 只 kill subagent 文件中记录的 ID
RUN_ID=<your run_id from runtime_state>
while read -r CHILD_ID; do
  ai kill --id "$CHILD_ID" 2>/dev/null
done < ~/.ai/runs/$RUN_ID/subagent
```

### ⛔ Dangerous patterns (NEVER DO THIS)

```bash
# ❌ 会杀掉所有 agent，包括不属于你的
ai kill --all

# ❌ 会杀掉不属于自己的 agent
ai kill --id <some-id-you-guessed>

# ❌ 会杀掉所有 tmux session，包括其他 agent 的终端
tmux kill-server

# ❌ 会杀掉不属于自己的 tmux session
tmux kill-session -t <session-not-yours>
```

### tmux kill-session 安全模式

只能 kill **session 名匹配 `agent-$RUN_ID-*` 的**：

```bash
# ✅ 安全：按命名前缀过滤
tmux list-sessions -F '#{session_name}' 2>/dev/null | grep "^agent-$RUN_ID-" | while read -r s; do
  tmux kill-session -t "$s" 2>/dev/null
done
```

## Timeout Guide

`--timeout` 用于 `ai serve`、`ai watch`、`ai send --wait`：

| 值 | 场景 |
|------|------|
| 不设置 | 无限运行（适合交互式多轮） |
| `--timeout 20m` | 最多等 20 分钟，超时退出 |
| `--timeout 0` | 永远等，直到进程退出或被 kill |

**⚠️ 按任务复杂度设 timeout——给得太短会导致"超时→打断→kill→重来"的恶性循环：**

| 任务类型 | 建议 timeout | 理由 |
|---------|-------------|------|
| 简单问答 / 状态查询 | `2m` | 单轮 LLM 调用 |
| 单文件修改 / 小 bug fix | `5m` | 少量 tool calls |
| 代码探索 / review（需 grep+read 多文件） | `10-15m` | 深度代码核查很慢，子 agent 会沉默很久 |
| 实现任务（多文件、测试） | `15-30m` | 多轮 tool 调用 + 验证 |

注意：`--timeout` 超时只影响 watch/send 命令退出，**不影响子 agent 进程**。超时后子 agent 仍在运行，需要 `ai kill` 清理。

## How to List Your Subagents

**只查看自己 spawn 的子 agent**（安全，不会误杀）：

```bash
RUN_ID=<your run_id from runtime_state>
cat ~/.ai/runs/$RUN_ID/subagent
```

**⚠️ 绝对禁止使用 `ai ls` 来决定 kill 目标。** `ai ls` 显示全局所有 agent，包括不属于你的。你只能 kill `~/.ai/runs/$RUN_ID/subagent` 文件中记录的 ID。

## Orphan Cleanup（孤儿清理）

如果主 agent 中断或崩溃，遗留运行中的子 agent：

1. 检查 `~/.ai/runs/$RUN_ID/subagent` 文件中的 ID
2. 逐一 `ai kill --id <id>` 清理
3. 清理对应的 tmux sessions（按 `agent-$RUN_ID-*` 前缀）
4. **报告给用户** 让用户知道清理了哪些

## ai serve Flags

| Flag | Description |
|------|-------------|
| `--system-prompt <string\|@file>` | Custom system prompt. `@file` reads file content. |
| `--input <string>` | Initial prompt to send after startup |
| `--input-file <path>` | Read initial prompt from file (avoids ARG_MAX) |
| `--name <string>` | Human-readable name |
| `--role <coder\|orchestrator\|validator>` | Agent role (affects system prompt) |
| `--timeout <duration>` | Total execution timeout (e.g., `10m`, `600s`) |
| `--session <path>` | Resume from existing session — **must be the session DIRECTORY path** (containing messages.jsonl), NOT the file path itself |
| `--max-turns <int>` | Max conversation turns |
| `--id-file <path>` | Write run ID to file after startup (for background mode) |

## ai send --wait Flags

| Flag | Description |
|------|-------------|
| `--wait` | 发送消息后阻塞等待 agent 处理完成，实时流式输出回复 |
| `--summary` | 只输出最终文本，不显示 tool calls/thinking |
| `--timeout <duration>` | 最多等待时间（`0` = 无限等待；`5m` = 最多 5 分钟） |
| `--id <string>` | 目标 agent 的 run ID |

**`send --wait` vs `send` + `watch`：** `send --wait` 内部先订阅事件流再发送消息，消除了 send→watch 之间的 race condition。一步到位。

## Edge Cases

### Rate Limit

子 agent 遇到 rate limit 时：
1. 自动重试最多 8 次，指数退避（最长 ~165 秒）
2. 每次重试发出 `llm_retry` 事件 → watch 可见：`ai: LLM retry 3/8 (rate_limit, waiting 12.0s)`
3. 全部重试耗尽后发出 `error` + `agent_end`

**主 agent 应对：** 看到 `llm_retry (rate_limit)` → 等待即可，子 agent 在自动处理。最终失败则报告给用户。

### 子 agent 完成但进程不退出

这是正常行为。`ai serve` 是后台服务，`agent_end` 不等于进程退出。watch 拿到结果后，**由调用方负责 `ai kill`**。

## Subagent as Context Firewall

每个子 agent 有独立的 context window：
- **隔离任务上下文** — 中间工具输出不污染主 agent 的 context
- **防止 context anxiety** — 每个子 agent 从满 context window 开始
- **专注 prompt** — 只给子 agent 完成任务所需的信息

## Error Handling

1. **报告给用户** — 不要静默重试
2. 用 `ai send --id "$CHILD_ID" --wait` 查看输出和获取回复
3. 让用户决定下一步
4. **无论成功或失败，都必须 cleanup** — 失败的 agent 也不会自动退出

## Common Pitfalls

| ❌ Wrong | ✅ Right |
|----------|----------|
| `ai serve ... &` (用 bash `&` 后台) | ⛔ **禁止用 `&`**，bash tool 的 pipe 会卡死；必须用 tmux |
| `ai serve ... \| head -1` (pipe breaks serve) | `--id-file` captures ID without pipe |
| watch 后不 kill | watch 返回后 kill subagent 文件中的所有 ID |
| spawn 空壳不带 `--input` | 推荐带 `--input`；如不带，务必安排后续 `ai send`，避免遗忘空跑 |
| 用 `ai ls` status 判断完成 | `ai serve` status 永远 `running`，用 `ai send --wait` 判断 |
| `ai send` + `ai watch` 两步操作 | `ai send --wait` 一步完成发送+等待回复 |
| `ai send --wait` 不带消息参数 | ⛔ **必须带消息**，否则报错 `no message provided`。写 `"请给出结果"` 即可 |
| kill 子 agent 后自己做它的活 | 用 `ai send --wait --summary` 询问进度，让子 agent 自己汇报 |
| `tmux kill-server` 清理环境 | ⛔ **绝对禁止**，会杀掉所有 tmux session |
| `ai ls` 看到就 kill | ⛔ **绝对禁止**，只 kill subagent 文件中记录的 ID |
| tmux session 名不含 `$RUN_ID` | 必须用 `agent-$RUN_ID-xxx` 格式，避免冲突 |
| 忘记写入 subagent 文件 | spawn 后必须 `echo "$CHILD_ID" >> ~/.ai/runs/$RUN_ID/subagent` |
| 用 `ai kill` 杀不属于自己的 agent | ⛔ 只 kill 自己 subagent 文件里的 ID |
| 长输出任务用 `send --wait --summary` 收集 | ⛔ `--summary` 截断长输出；长任务用**方式 C（写文件）** |
| 用 `ai send` 查询正在干活的子 agent 进度 | ⛔ `send` 会注入新 prompt **打断**子 agent；用 `watch --follow --pretty` 观察 |
| `ai watch` 不加 `--pretty` | ⛔ 非 TUI 环境（tmux/脚本）不加 `--pretty` 会无输出 |
| timeout 设太短（如 review 任务设 60s） | ⛔ 超时→打断→kill→重来循环；按任务类型设（探索/review 至少 10m） |
| kill 前不确认子 agent 是否在推进 | kill 前 `watch --follow --pretty` 确认是否真的卡死 |

## Relationship to Other Skills

本技能是子 agent 操作的**单一事实来源**。其他技能引用本技能即可，无需重复定义 spawn/watch/kill 流程。

| Skill | Uses subagent for |
|-------|-------------------|
| `pge` | Generator and Validator agents |
| `explore` | Parallel codebase exploration |
| `debate` | Proposer and opposer agents |
| `review` | Reviewer agent |
| `worker-judge` | Worker and judge agents |
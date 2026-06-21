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

每个子 agent 必须经历 Spawn → Wait → Cleanup 完整生命周期。根据任务传递方式选择路径：

### 路径 A（推荐）：spawn 时传任务 → watch 等待

```
┌─────────────────┐     ┌───────────────────┐     ┌──────────┐
│ Spawn           │────►│ Watch Loop        │────►│ Cleanup  │
│ (--input-file)  │     │ (watch --follow)  │     │ (kill)   │
└─────────────────┘     └───────────────────┘     └──────────┘
```

spawn 时通过 `--input-file` 传入任务。子 agent 收到后自动开始处理，用 `watch --follow --pretty` 观察直到完成。**不需要 `send`。**

### 路径 B：spawn 空壳 → send 发任务 → watch 等待

```
┌─────────────────┐     ┌───────────────────┐     ┌───────────────────┐     ┌──────────┐
│ Spawn           │────►│ Send + Wait       │────►│ Watch Loop        │────►│ Cleanup  │
│ (不带 --input)  │     │ (send --wait)     │     │ (watch --follow)  │     │ (kill)   │
└─────────────────┘     └───────────────────┘     └───────────────────┘     └──────────┘
```

spawn 时不传任务，然后用 `send --wait` 发送任务。`send --wait` 同时起到**原子握手**的作用——确认子 agent 已启动并响应。

> **为什么 `send` 存在？** spawn 和 watch 不是原子操作：spawn 后立即 watch，可能因子 agent 尚未就绪而空返回，主 agent 误判为失败。`send --wait` 内部先订阅事件流再发送消息，保证子 agent 已响应后才返回，消除了这个 race condition。
>
> **但 `send` 会注入新 prompt。** 对路径 A（已通过 `--input-file` 传了任务），用 `send` 会打断正在执行的任务。路径 A 直接用 watch 即可。

### 阶段 1: Spawn

**⚠️ `ai serve` 是阻塞命令，必须用 tmux 后台运行。** 不能用 `&`（bash tool 的 pipe 会卡死）。

**💡 `--input` 建议：** 推荐在 spawn 时通过 `--input` 或 `--input-file` 传入任务指令。不带 `--input` spawn 空壳是支持的用法（如预热、条件分发、多轮交互），但务必在流程中安排好后续 `ai send`，避免遗忘导致空跑浪费 token。

**⚠️ 推荐用 `--role`，避免手写 `--system-prompt`：** 大多数场景应使用 `--role coder`（默认值），`ai` 会自动加载对应的 system prompt。仅在需要高度定制化的 role（如 validator with specific checklist）时才用 `--system-prompt`。注意：同时设置两者时，`--system-prompt` 会覆盖 `--role`。

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

### 阶段 2: 等待子 agent 完成

#### Watch Loop（标准等待方式）

`watch --follow --pretty` 是等待子 agent 的标准方式。它纯观察，不注入 prompt，不打断子 agent。

**不需要预估任务时长。** 每轮 watch 是一个观察窗口，通过循环判断子 agent 是否在推进：

```bash
# 第一轮 watch（bash tool timeout 或 --timeout 控制单轮观察时长）
ai watch --id "$CHILD_ID" --follow --pretty --timeout 5m

# watch 返回后（timeout 或 agent 完成），检查是否还在推进：
# - watch 输出中有 tool call / thinking → 在干活 → 再 watch 一轮
# - git diff 有新变化 → 在干活 → 再 watch 一轮
# - 完全无输出且无变化 → 可能卡死 → 参考"卡死判断"章节

# 继续观察
ai watch --id "$CHILD_ID" --follow --pretty --timeout 5m
```

**关键认知：**
- watch 超时（bash tool 限制或 `--timeout`）≠ 子 agent 失败。子 agent 在 tmux 中继续运行。
- 不需要一次性 watch 到完成。分轮观察、每轮判断是否继续，更可控。
- 子 agent 等 LLM 响应时（如 rate limit retry）可能几分钟没输出但不是卡死。结合 `git diff` 判断。

#### send --wait 的正确用途

`send --wait` 会注入新 prompt，**不是标准等待方式**。只在以下场景使用：

1. **路径 B 的原子握手**：spawn 空壳后，用 `send --wait` 发送任务并确认子 agent 已响应
2. **多轮指令**：给正在运行的子 agent 发新指令（如 PGE 中 Evaluator 反馈后让 Generator 修复）

```bash
# 路径 B：发任务 + 确认子 agent 活着
ai send --id "$CHILD_ID" --wait --timeout 5m "Fix the bug in auth.go"

# 多轮：追加反馈
ai send --id "$CHILD_ID" --wait --timeout 10m "Also handle nil input case"
```

**⚠️ 不要用 `send --wait` 收集 `--input-file` 任务的回复。** 那会注入多余 prompt 打断正在执行的任务。用 watch 观察即可。

#### 长输出任务：写文件 + Watch

预期输出很长（review、探索、设计文档）时，在任务 prompt 中要求子 agent 把结果写入文件，避免 `send --wait --summary` 的截断问题：

```bash
# spawn 时在 --input 中指示写文件
ai serve --role coder \
  --input '...Write your complete output to /tmp/result.md...Output DONE when complete.' \
  ...

# watch 观察进度（不打断）
ai watch --id "$CHILD_ID" --follow --pretty --timeout 5m

# 完成后读文件（完整内容，无截断）
cat /tmp/result.md
```

### 阶段 3: 多轮交互（可选）

需要给正在运行的子 agent 发新指令时，用 `send --wait`（参见上方"send --wait 的正确用途"）。

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
| **任务确认完成后立即 kill** | 延迟 kill = 忘记 kill。watch 超时不等于完成——先检查再决定 |
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

## Timeout & Watch Loop 策略

**核心原则：不预估任务时长，用 watch loop 检测推进。**

### `ai serve --timeout`

设大值（如 `30m`）或不设置。不要用 serve 超时来控制流程——子 agent 被超时杀死会导致工作丢失。

### `ai watch --timeout`

控制单轮观察窗口（建议 `5m`）。watch 超时后控制权回到主 agent，子 agent 仍在 tmux 中继续运行。主 agent 判断是否需要再 watch 一轮。

### 卡死判断

连续两轮 watch（~10 分钟）无 tool call 输出 **且** `git diff` 无变化 → 可能卡死，考虑 kill。注意 rate limit retry 期间也会无输出，但不是卡死——watch 中会显示 `llm_retry` 事件。

### 超时恢复

watch 超时后：
1. ❌ 不要立即 kill
2. ✅ `git diff --stat` 检查子 agent 是否已产出文件
3. ✅ 有产出 → 子 agent 在推进 → 再 watch 一轮
4. ✅ 无产出且无输出 → 可能卡死 → kill
5. ✅ kill 后如果发现有产出 → 在此基础上继续，**不要从零重做**

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
2. 用 `ai watch --id "$CHILD_ID" --follow --pretty` 观察子 agent 状态
3. 让用户决定下一步
4. **无论成功或失败，都必须 cleanup** — 失败的 agent 也不会自动退出

## Common Pitfalls

| ❌ Wrong | ✅ Right |
|----------|----------|
| `ai serve ... &` (用 bash `&` 后台) | ⛔ **禁止用 `&`**，bash tool 的 pipe 会卡死；必须用 tmux |
| `ai serve ... \| head -1` (pipe breaks serve) | `--id-file` captures ID without pipe |
| watch 后不 kill | watch 返回后 kill subagent 文件中的所有 ID |
| spawn 空壳不带 `--input` | 推荐带 `--input`；如不带，务必安排后续 `ai send`，避免遗忘空跑 |
| 用 `ai ls` status 判断完成 | `ai serve` status 永远 `running`，用 `watch --follow` 或 `send --wait` 判断 |
| `ai send` + `ai watch` 两步操作 | `ai send --wait` 一步完成发送+等待回复 |
| `ai send --wait` 不带消息参数 | ⛔ **必须带消息**，否则报错 `no message provided`。写 `"请给出结果"` 即可 |
| kill 子 agent 后自己做它的活 | 用 `ai watch --follow` 观察进度，让子 agent 自己完成 |
| `tmux kill-server` 清理环境 | ⛔ **绝对禁止**，会杀掉所有 tmux session |
| `ai ls` 看到就 kill | ⛔ **绝对禁止**，只 kill subagent 文件中记录的 ID |
| tmux session 名不含 `$RUN_ID` | 必须用 `agent-$RUN_ID-xxx` 格式，避免冲突 |
| 忘记写入 subagent 文件 | spawn 后必须 `echo "$CHILD_ID" >> ~/.ai/runs/$RUN_ID/subagent` |
| 用 `ai kill` 杀不属于自己的 agent | ⛔ 只 kill 自己 subagent 文件里的 ID |
| 长输出任务用 `send --wait --summary` 收集 | ⛔ `--summary` 截断长输出；长任务用写文件 + watch |
| 用 `ai send` 查询正在干活的子 agent 进度 | ⛔ `send` 会注入新 prompt **打断**子 agent；用 `watch --follow --pretty` 观察 |
| `ai watch` 不加 `--pretty` | ⛔ 非 TUI 环境（tmux/脚本）不加 `--pretty` 会无输出 |
| watch 超时后立即 kill | ⛔ 超时 ≠ 失败；先 `git diff` 检查产出，有变化就再 watch 一轮 |
| kill 前不确认子 agent 是否在推进 | 连续两轮 watch 无输出且 `git diff` 无变化才考虑 kill |
| kill 后发现有产出却从零重做 | ⛔ 先 `git diff` 检查子 agent 产出，在此基础上继续 |

## Relationship to Other Skills

本技能是子 agent 操作的**单一事实来源**。其他技能引用本技能即可，无需重复定义 spawn/watch/kill 流程。

| Skill | Uses subagent for |
|-------|-------------------|
| `pge` | Generator and Validator agents |
| `explore` | Parallel codebase exploration |
| `debate` | Proposer and opposer agents |
| `review` | Reviewer agent |
| `worker-judge` | Worker and judge agents |
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

## Agent Lifecycle（完整生命周期）

每个子 agent 必须经历完整的生命周期，不得跳过任何阶段：

```
┌─────────┐     ┌─────────┐     ┌─────────┐     ┌──────────┐
│  Spawn  │────►│  Watch  │────►│Collect   │────►│ Cleanup  │
│ (tmux)  │     │(follow) │     │Result    │     │(kill+rm) │
└─────────┘     └─────────┘     └─────────┘     └──────────┘
```

### 阶段 1: Spawn

`ai serve` 是**阻塞命令**。直接调用会卡死主 agent。必须用 tmux 后台运行。

**⚠️ `--input` 是 MUST：** spawn 时必须通过 `--input` 或 `--input-file` 传入任务指令。**禁止** spawn 空壳后想 "等会儿再 send"——这是最常见的遗忘源头。

```bash
SESSION="my-agent"

# 确保不会重名
tmux kill-session -t "$SESSION" 2>/dev/null

# ✅ 正确：--input 在 spawn 时就传入任务
tmux new-session -d -s "$SESSION" \
  "ai serve --system-prompt 'You are a coding assistant.' \
   --input 'Fix the bug in auth.go' \
   --name 'fix-auth'"

# Capture RUN_ID（ai serve 输出的第一行）
sleep 2
RUN_ID=$(tmux capture-pane -t "$SESSION" -p | head -1 | tr -d '[:space:]')
```

**如果任务描述很长**（超过 shell ARG_MAX），用 `--input-file`：
```bash
echo "$LONG_TASK_PROMPT" > /tmp/task-prompt.txt
tmux new-session -d -s "$SESSION" \
  "ai serve --system-prompt 'You are a coder.' \
   --input-file /tmp/task-prompt.txt \
   --name 'long-task'"
```

### 阶段 2: Watch（--input 初始任务）

spawn 时通过 `--input` 传入的任务，用 `watch --follow --pretty` 等待完成：

```bash
# 默认：agent_end 时返回
ai watch --id "$RUN_ID" --follow --pretty

# 或设超时
ai watch --id "$RUN_ID" --follow --pretty --timeout 20m
```

**后续轮次用 `ai send --wait`**（见 Multi-Turn 模式），一步完成发送+等待，不需要单独 watch。

### 阶段 3: Collect Result

watch 或 `send --wait` 返回后，子 agent 的输出已直接获得。

### 阶段 4: Cleanup（⚠️ 必须执行）

**`ai serve` 是长驻进程，不会自动退出。** `agent_end` 只代表当前 prompt 处理完，不代表进程退出。watch 返回后，**必须**显式清理：

```bash
ai kill --id "$RUN_ID" 2>/dev/null
tmux kill-session -t "$SESSION" 2>/dev/null
```

**为什么不自动退出：** `ai serve` 的设计就是后台服务模式，可以接收多轮 `ai send`。退出应由调用方（即你）负责。这是 `ai serve` 的正确行为。

### 生命周期铁律

| 规则 | 违反后果 |
|------|---------|
| **每个 spawn 必须对应一个 kill** | 进程堆积，内存泄漏（每个 ~20MB） |
| **watch 返回后立即 kill** | 延迟 kill = 忘记 kill |
| **异常路径也要 kill** | 主 agent 崩溃会留下孤儿进程 |
| **kill 顺序：先 `ai kill`，再 `tmux kill-session`** | 反过来可能导致僵尸 tmux session |

### ⛔ Tmux Cleanup Safety

> **Agent 误操作 `tmux kill-server` 曾导致用户丢失全部 tmux session。**

| 禁令 | 原因 |
|------|------|
| **禁止 `tmux kill-server`** | 销毁整个 tmux 服务器，用户所有 session 全灭 |
| **禁止遍历所有 session 并批量 kill** | 通配符可能命中用户 session；最后一个 session 被杀后 server 自动退出 |
| **禁止 kill 非本 agent 创建的 session** | 你不知道其他 session 的用途 |
| **禁止向唯一 pane 发送 `exit` / `C-d`** | pane → window → session 连锁退出 |

```bash
# ✅ 正确：只清理你自己创建的 session（用你起的名字）
ai kill --id "$RUN_ID" 2>/dev/null
tmux kill-session -t "$SESSION" 2>/dev/null

# ❌ 致命错误：kill-server 会毁掉用户的全部工作环境
tmux kill-server

# ❌ 高风险：遍历+通配符 kill 可能误杀用户 session
for s in $(tmux list-sessions | grep ...); do tmux kill-session -t "$s"; done
```

### ⛔ Kill Ownership Rule（最关键的安全规则）

> **你只能 kill 你自己 spawn 的 agent。`ai ls` 看到的其他 agent 可能是用户、其他 PGE 流程、或其他工具启动的。**

**PGE 开始前检查孤儿的正确做法**：
- `ai ls` 只是**观察**，了解环境状态
- 如果发现可能有前次 PGE 遗留的孤儿（通过 agent name 或 session name 匹配），**报告给用户让用户决定**，不要自行 kill
- 你只维护一个 `SPAWNED_IDS` 列表，cleanup 时只 kill 列表中的 ID

```bash
# ✅ 正确：跟踪自己 spawn 的 agent
SPAWNED_IDS=()
SPAWNED_SESSIONS=()

# spawn 时记录
tmux new-session -d -s "gen-001" "ai serve ..."
RUN_ID=$(tmux capture-pane -t "gen-001" -p | head -1 | tr -d '[:space:]')
SPAWNED_IDS+=("$RUN_ID")
SPAWNED_SESSIONS+=("gen-001")

# cleanup 时只清理自己记录的
for id in "${SPAWNED_IDS[@]}"; do ai kill --id "$id" 2>/dev/null; done
for sess in "${SPAWNED_SESSIONS[@]}"; do tmux kill-session -t "$sess" 2>/dev/null; done

# ❌ 致命错误：看到 ai ls 有 agent 就 kill
ai ls | awk '{print $2}' | while read name; do ai kill --id "$name"; done
# ↑ 这会杀掉用户正在使用的其他 agent！包括可能杀掉你自己！

# ❌ 致命错误：PGE 开始前 "清理环境"
ai ls --json | jq -r '.[].id' | while read id; do ai kill --id "$id"; done
# ↑ 这会杀掉所有 agent，包括你自己（orchestrator）！
```

## Pattern: One-Shot（最常用）

```bash
SESSION="my-agent"

# 1. Spawn
tmux kill-session -t "$SESSION" 2>/dev/null
tmux new-session -d -s "$SESSION" \
  "ai serve --system-prompt 'You are a coding assistant.' \
   --input 'Fix the bug in auth.go' \
   --name 'fix-auth'"
sleep 2
RUN_ID=$(tmux capture-pane -t "$SESSION" -p | head -1 | tr -d '[:space:]')

# 2. Watch
ai watch --id "$RUN_ID" --follow --pretty

# 3. Cleanup
ai kill --id "$RUN_ID" 2>/dev/null
tmux kill-session -t "$SESSION" 2>/dev/null
```

## Pattern: Multi-Turn（send --wait）

需要多轮交互时，用 `ai send --wait` 一步完成发送+等待回复：

```bash
tmux kill-session -t "worker" 2>/dev/null
tmux new-session -d -s "worker" \
  "ai serve --system-prompt 'You are a coder.' \
   --input 'Read auth.go and identify the bug' \
   --name 'fix-auth'"
sleep 2
RUN_ID=$(tmux capture-pane -t "worker" -p | head -1 | tr -d '[:space:]')

# 第一轮：watch 初始任务结果
ai watch --id "$RUN_ID" --follow --pretty

# 第二轮：send --wait 一步发送+等待
ai send --id "$RUN_ID" --wait "Now fix it and write tests"

# 第三轮（如果需要）
ai send --id "$RUN_ID" --wait --summary "Brief summary of what you changed"

# 收工 — 必须清理
ai kill --id "$RUN_ID"
tmux kill-session -t "worker"
```

## Pattern: Parallel（最多 2 个子 agent）

```bash
# Spawn
for name in agent-a agent-b; do
  tmux kill-session -t "$name" 2>/dev/null
done
tmux new-session -d -s "agent-a" \
  "ai serve --system-prompt '@$HOME/.ai/skills/explore/explorer.md' \
   --input 'Explore the auth module. Write findings to: /tmp/explore-auth.md' \
   --name 'explore-auth'"
tmux new-session -d -s "agent-b" \
  "ai serve --system-prompt '@$HOME/.ai/skills/explore/explorer.md' \
   --input 'Explore the RPC layer. Write findings to: /tmp/explore-rpc.md' \
   --name 'explore-rpc'"

sleep 2
ID_A=$(tmux capture-pane -t "agent-a" -p | head -1 | tr -d '[:space:]')
ID_B=$(tmux capture-pane -t "agent-b" -p | head -1 | tr -d '[:space:]')

# Watch：交替短轮询
# ❌ 错误：串行 watch（第一个会阻塞，第二个无人看管）
# ai watch --id "$ID_A" --follow --pretty   ← blocks until A finishes
# ai watch --id "$ID_B" --follow --pretty   ← B might be stuck/done already

# ✅ 正确：用 --timeout 短轮询交替检查
ai watch --id "$ID_A" --follow --pretty --timeout 2m
ai watch --id "$ID_B" --follow --pretty --timeout 2m
# 重复直到两个都收到 agent_end，或达到最大等待时间

# Cleanup — 全部完成后统一清理
ai kill --id "$ID_A" 2>/dev/null; ai kill --id "$ID_B" 2>/dev/null
tmux kill-session -t "agent-a" 2>/dev/null; tmux kill-session -t "agent-b" 2>/dev/null
```

### 并行进度观察（Parallel Progress Check）

当子 agent 长时间运行时，用 `ai send --wait --timeout` 询问进度并等待回复：

```bash
# 向两个 agent 发送进度查询并等待回复
ai send --id "$ID_A" --wait --summary --timeout 30s 'Progress check: brief status.'
ai send --id "$ID_B" --wait --summary --timeout 30s 'Progress check: brief status.'
```

## Watch Timeout 详解

**`--timeout` 的三种用法：**

| 用法 | 行为 |
|------|------|
| 不设 | agent_end 时退出（默认，适合一次性任务） |
| `--timeout 20m` | 最多等 20 分钟，超时退出 |
| `--timeout 0` | 永远等，直到进程退出或被 kill |

注意：`--timeout` 超时只影响 watch 命令退出，**不影响子 agent 进程**。超时后子 agent 仍在运行，需要 `ai kill` 清理。

## Orphan Cleanup（孤儿清理）

如果主 agent 中断或崩溃，遗留运行中的子 agent：

```bash
ai ls                    # 列出所有运行中的 agent（仅观察）
```

**⚠️ 绝对禁止批量 kill `ai ls` 看到的 agent。** 你只能 kill 你自己 spawn 的 agent（通过 `SPAWNED_IDS` 列表跟踪）。`ai ls` 中的 agent 可能是用户、其他 PGE 流程、或当前 agent 自己启动的。

**正确的孤儿处理**：
1. `ai ls` 查看，识别可能的孤儿（通过 name 匹配你之前的 session 命名模式）
2. **报告给用户**，让用户决定是否清理
3. 如果用户确认清理特定 agent，才执行 `ai kill --id <that-specific-id>`

## ai serve Flags

| Flag | Description |
|------|-------------|
| `--system-prompt <string\|@file>` | Custom system prompt. `@file` reads file content. |
| `--input <string>` | Initial prompt to send after startup |
| `--input-file <path>` | Read initial prompt from file (avoids ARG_MAX) |
| `--name <string>` | Human-readable name |
| `--role <coder\|orchestrator\|validator>` | Agent role (affects system prompt) |
| `--timeout <duration>` | Total execution timeout (e.g., `10m`, `600s`) |
| `--session <path>` | Resume from existing session file |
| `--max-turns <int>` | Max conversation turns |

## ai send --wait Flags

| Flag | Description |
|------|-------------|
| `--wait` | 发送消息后阻塞等待 agent 处理完成，实时流式输出回复 |
| `--summary` | 只输出最终文本，不显示 tool calls/thinking |
| `--timeout <duration>` | 最多等待时间（`0` = 无限等待；`5m` = 最多 5 分钟） |
| `--id <string>` | 目标 agent 的 run ID |

**`send --wait` vs `send` + `watch`：** `send --wait` 内部先订阅事件流再发送消息，消除了 send→watch 之间的 race condition。一步到位，不需要 tmux 来同时跑 watch。

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
2. 用 `ai send --id "$RUN_ID" --wait` 查看输出和获取回复
3. 让用户决定下一步
4. **无论成功或失败，都必须 cleanup** — 失败的 agent 也不会自动退出

## Common Pitfalls

| ❌ Wrong | ✅ Right |
|----------|----------|
| `ai serve --input "..." && echo done` | `tmux new-session -d -s X "ai serve ..."` |
| `ai serve ... \| head -1` | `tmux capture-pane -t SESSION -p \| head -1` |
| watch 后不 kill | watch 返回后 `ai kill` + `tmux kill-session` |
| tmux session 已存在 | `tmux kill-session -t NAME 2>/dev/null; tmux new-session -d -s NAME ...` |
| 只 kill tmux 不 kill ai | 先 `ai kill`，再 `tmux kill-session`（顺序重要） |
| spawn 前不清理同名 session | 先 `tmux kill-session -t NAME 2>/dev/null` 再 spawn |
| spawn 空壳（不带 `--input`）再想 send | **spawn 时必须 `--input` 传任务**，避免遗忘 |
| 用 `ai ls` status 判断完成 | `ai serve` status 永远 `running`，用 `ai send --wait` 判断 |
| `ai send` + `ai watch` 两步操作 | `ai send --wait` 一步完成发送+等待回复 |
| kill 子 agent 后自己做它的活 | 用 `ai send --wait --summary` 询问进度，让子 agent 自己汇报 |
| `tmux kill-server` 清理环境 | ⛔ **绝对禁止**，只允许 `kill-session -t <你的session名>` |
| `ai ls` 看到就 kill | ⛔ **绝对禁止**，只 kill 自己 spawn 的 agent |

## Relationship to Other Skills

本技能是子 agent 操作的**单一事实来源**。其他技能引用本技能即可，无需重复定义 spawn/watch/kill 流程。

| Skill | Uses subagent for |
|-------|-------------------|
| `pge` | Generator and Validator agents |
| `explore` | Parallel codebase exploration |
| `debate` | Proposer and opposer agents |
| `review` | Reviewer agent |
| `worker-judge` | Worker and judge agents |
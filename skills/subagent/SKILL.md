---
name: subagent
description: 使用 ai serve/send/watch/kill 控制子 agent 的通用指引。所有需要子 agent 的技能都应参考此技能。
---

# Subagent Operations Guide

通过 `ai` CLI 的 `serve`/`send`/`watch`/`kill` 控制子 agent，实现任务委派和并行执行。

## When to Use

- 需要将任务委派给独立 agent（探索、实现、审查、验证等）
- 需要并行执行多个独立子任务
- 需要隔离上下文（子 agent 不继承主 agent 的对话历史）

**不要用于：** 简单的 bash 命令（直接执行即可）

## ⚠️ Concurrency Limit

**主 agent + 子 agent 同时运行总数不得超过 3**（即最多 2 个子 agent 同时运行）。
LLM 提供商限流：并发稍高即触发 API rate limit，导致子 agent 卡住或失败。

## Core Pattern: Spawn → Watch → Cleanup

`ai serve` 是**阻塞命令**。直接调用会卡死主 agent。必须用 tmux 后台运行。

### One-Shot Pattern（最常用）

```bash
SESSION="my-agent"
RUN_ID=""

# 1. Spawn: tmux 后台启动（不需要 --timeout）
tmux new-session -d -s "$SESSION" \
  "ai serve --system-prompt 'You are a coding assistant.' \
   --input 'Fix the bug in auth.go' \
   --name 'fix-auth'"

# 2. Capture RUN_ID
sleep 2
RUN_ID=$(tmux capture-pane -t "$SESSION" -p | head -1 | tr -d '[:space:]')

# 3. Watch: 默认在 agent_end 时退出
ai watch --id "$RUN_ID" --follow --pretty

# 4. Cleanup（watch 返回后子 agent 进程仍在，需 kill）
ai kill --id "$RUN_ID" 2>/dev/null
tmux kill-session -t "$SESSION" 2>/dev/null
```

### Watch Timeout（等不了就放弃）

```bash
# 最多等 20 分钟，超时 watch 退出，子 agent 不受影响
ai watch --id "$RUN_ID" --follow --pretty --timeout 20m

# 等到天荒地老（直到 kill 或进程崩溃）
ai watch --id "$RUN_ID" --follow --pretty --timeout 0
```

**`--timeout` 的三种用法：**

| 用法 | 行为 |
|------|------|
| 不设 | agent_end 时退出（默认，适合一次性任务） |
| `--timeout 20m` | 最多等 20 分钟，超时退出 |
| `--timeout 0` | 永远等，直到进程退出或被 kill |

### Multi-Turn Pattern（send + watch 交替）

需要多轮交互时，用 `--timeout 0` 让 watch 跨越多个 agent_end：

```bash
tmux new-session -d -s "worker" \
  "ai serve --system-prompt 'You are a coder.'"
sleep 2
RUN_ID=$(tmux capture-pane -t "worker" -p | head -1 | tr -d '[:space:]')

# 第一轮
ai send --id "$RUN_ID" "Read auth.go and identify the bug"
ai watch --id "$RUN_ID" --follow --pretty    # 默认行为，agent_end 时退出

# 第二轮
ai send --id "$RUN_ID" "Now fix it and write tests"
ai watch --id "$RUN_ID" --follow --pretty    # agent_end 时退出

# 或者：发完所有指令后，一次 watch 等全部完成
ai send --id "$RUN_ID" "Fix bug A"
ai send --id "$RUN_ID" "Also fix bug B"
ai watch --id "$RUN_ID" --follow --pretty --timeout 0   # 跨越多个 agent_end

# 收工
ai kill --id "$RUN_ID"
tmux kill-session -t "worker"
```

### Poll（非阻塞快照）

快速看一眼当前状态，不阻塞：

```bash
# 看全部已有输出
ai watch --id "$RUN_ID" --since 0
```

### Parallel Pattern（最多 2 个子 agent）

```bash
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

ai watch --id "$ID_A" --follow --pretty
ai watch --id "$ID_B" --follow --pretty

ai kill --id "$ID_A" 2>/dev/null; ai kill --id "$ID_B" 2>/dev/null
tmux kill-session -t "agent-a" 2>/dev/null; tmux kill-session -t "agent-b" 2>/dev/null
```

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

## ai watch --follow Flags

| Flag | Description |
|------|-------------|
| `--pretty` | Format output as readable conversation |
| `--timeout <duration>` | Max wait time. 不设 = agent_end 退出; `0` = 永远等; `20m` = 最多 20 分钟 |

## Edge Cases

### Rate Limit

子 agent 遇到 rate limit 时：
1. 自动重试最多 8 次，指数退避（最长 ~165 秒）
2. 每次重试发出 `llm_retry` 事件 → watch 可见：`ai: LLM retry 3/8 (rate_limit, waiting 12.0s)`
3. 全部重试耗尽后发出 `error` + `agent_end`

**主 agent 应对：** 看到 `llm_retry (rate_limit)` → 等待即可，子 agent 在自动处理。最终失败则报告给用户。

### 子 agent 完成但进程不退出

`ai serve` 是长驻进程。agent_end 只代表当前 prompt 处理完，不代表进程退出。

- **默认 watch**：agent_end 时退出，拿到结果，不需要管进程
- **必须 cleanup**：watch 返回后用 `ai kill` + `tmux kill-session` 清理进程

### 孤儿清理

如果主 agent 中断，遗留运行中的子 agent：

```bash
ai ls                    # 列出所有运行中的 agent
ai kill --id <orphan>    # 清理
```

## Subagent as Context Firewall

每个子 agent 有独立的 context window：
- **隔离任务上下文** — 中间工具输出不污染主 agent 的 context
- **防止 context anxiety** — 每个子 agent 从满 context window 开始
- **专注 prompt** — 只给子 agent 完成任务所需的信息

## Error Handling

1. **报告给用户** — 不要静默重试
2. 用 `ai watch --id "$RUN_ID" --since 0` 查看输出
3. 让用户决定下一步

## Common Pitfalls

| ❌ Wrong | ✅ Right |
|----------|----------|
| `ai serve --input "..." && echo done` | `tmux new-session -d -s X "ai serve ..."` |
| `ai serve ... \| head -1` | `tmux capture-pane -t SESSION -p \| head -1` |
| watch 后不 kill | watch 返回后 `ai kill` + `tmux kill-session` |
| tmux session 已存在 | `tmux kill-session -t NAME 2>/dev/null; tmux new-session -d -s NAME ...` |

## Relationship to Other Skills

| Skill | Uses subagent for |
|-------|-------------------|
| `pge` | Generator and Validator agents |
| `explore` | Parallel codebase exploration |
| `debate` | Proposer and opposer agents |
| `review` | Reviewer agent |
| `worker-judge` | Worker and judge agents |
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

`ai serve` 是**后台守护进程**，启动后立即返回，进程在后台持续运行。

### One-Shot Pattern（最常用）

```bash
RUN_ID="my-task-$(date +%Y%m%d_%H%M%S)_$$"

# 1. Spawn: 启动后台守护进程（指定 ID）
ai serve --id "$RUN_ID" \
  --system-prompt 'You are a coding assistant.' \
  --input 'Fix the bug in auth.go' \
  --name 'fix-auth'

# 进程在后台运行，调用者立即返回

# 2. Watch: 连接到守护进程
ai watch --id "$RUN_ID" --follow --pretty

# 3. Cleanup
ai kill --id "$RUN_ID"
```

### Watch Timeout（等不了就放弃）

```bash
# 最多等 20 分钟
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

需要多轮交互时，保持守护进程运行，多次 send：

```bash
RUN_ID="worker-$(date +%Y%m%d_%H%M%S)_$$"

# 启动守护进程
ai serve --id "$RUN_ID" \
  --system-prompt 'You are a coder.'

# 第一轮
ai send --id "$RUN_ID" "Read auth.go and identify the bug"
ai watch --id "$RUN_ID" --follow --pretty

# 第二轮
ai send --id "$RUN_ID" "Now fix it and write tests"
ai watch --id "$RUN_ID" --follow --pretty

# 收工
ai kill --id "$RUN_ID"
```

### Parallel Pattern（最多 2 个子 agent）

```bash
ID_A="task-a-$(date +%Y%m%d_%H%M%S)_$$"
ID_B="task-b-$(date +%Y%m%d_%H%M%S)_$$"

ai serve --id "$ID_A" \
  --system-prompt '@$HOME/.ai/skills/explore/explorer.md' \
  --input 'Explore the auth module. Write findings to: /tmp/explore-auth.md' \
  --name 'explore-auth'

ai serve --id "$ID_B" \
  --system-prompt '@$HOME/.ai/skills/explore/explorer.md' \
  --input 'Explore the RPC layer. Write findings to: /tmp/explore-rpc.md' \
  --name 'explore-rpc'

ai watch --id "$ID_A" --follow --pretty
ai watch --id "$ID_B" --follow --pretty

ai kill --id "$ID_A" 2>/dev/null
ai kill --id "$ID_B" 2>/dev/null
```

### Poll（非阻塞快照）

快速看一眼当前状态，不阻塞：

```bash
# 看全部已有输出
ai watch --id "$RUN_ID" --since 0
```

## ai serve Flags

| Flag | Description |
|------|-------------|
| `--id <run-id>` | **Run ID（必须由调用者提供）** |
| `--system-prompt <string\|@file>` | Custom system prompt. `@file` reads file content. |
| `--input <string>` | Initial prompt to send after startup |
| `--input-file <path>` | Read initial prompt from file (avoids ARG_MAX) |
| `--name <string>` | Human-readable name |
| `--role <coder\|orchestrator\|validator>` | Agent role (affects system prompt) |
| `--timeout <duration>` | Total execution timeout (e.g., `10m`, `600s`) |
| `--session <path>` | Resume from existing session file |
| `--max-turns <int>` | Max conversation turns |

**关键变化：** `ai serve` 不再自动生成 ID，调用者必须通过 `--id` 指定。启动后立即返回，进程在后台守护运行。

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

### 子 agent 完成但守护进程不退出

`ai serve` 是守护进程，不会自动退出。需要手动 kill：

- **默认 watch**：agent_end 时退出，拿到结果，守护进程继续运行
- **必须 cleanup**：用 `ai kill` 清理守护进程

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
| `ai serve --input "..."` | `ai serve --id "$RUN_ID" --input "..."` |
| 等待 `ai serve` 返回 ID | 调用者自己生成 ID |
| `ai serve ... &` | `ai serve --id "$RUN_ID" ...` (自动后台) |
| watch 后不 kill | watch 返回后 `ai kill` |
| tmux 启动 subagent | 直接 `ai serve --id "$RUN_ID"` |

## Relationship to Other Skills

| Skill | Uses subagent for |
|-------|-------------------|
| `pge` | Generator and Validator agents |
| `explore` | Parallel codebase exploration |
| `debate` | Proposer and opposer agents |
| `review` | Reviewer agent |
| `worker-judge` | Worker and judge agents |
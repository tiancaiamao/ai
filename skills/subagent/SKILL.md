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

**同时运行的子 agent 不得超过 5 个**（主 agent 不计入）。

原因：LLM 提供商限流——并发稍高即触发 API rate limit，导致子 agent 卡住或失败。

**本技能是并发上限的唯一出处**：`pge`、`explore` 等其他技能只引用此处，不重复给出数字。

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

**默认路线：直接前台启动，靠 bash 工具的 5s 自动后台。不要 tmux，也不要 `&`。**

bash 工具对任何超过 5s 的命令自动 detach、返回 `op-N` handle，命令结束时把输出作为后续消息投递。因此 `ai serve` 直接启动即可。

> ⚠️ **必须显式传 `timeout: 0`。** 不传 = 默认 120s；到点时 bash 工具 SIGKILL 的是 `sh -c` 包装进程，而 `ai serve` 启动即 `setpgid` 到独立进程组（`subcommand/run/run.go`），所以 serve **不会**被杀，而是变成 ppid=1 的孤儿继续 running，父 agent 收到的完成通知却写着"被 kill"——状态与现实相反（实测 2026-11-05：`timeout: 8` → 8s 后包装进程死、serve 存活）。
>
> 同理 **`ai watch`、`sleep 5+` 自己也会被 5s 规则 detach**：等待不靠命令阻塞（结果走后续消息），检查类命令必须短（`cat` / `ps -p` / `ai ls`）。

**💡 `--input` 建议：** 推荐在 spawn 时通过 `--input-file` 传入任务指令（**不要用 `--input` 传长文本**）。`--input` 内联长文本要经 `/bin/sh -c` 解析 + 多行转义，极易出错导致 serve 立即退出（session 找不到）。规则：

- **短指令（<100 chars）**：`--input 'Fix the bug in auth.go'` 即可
- **多行/长文本（>100 chars 或含换行/引号/特殊字符）**：⚠️ **必须**先写入 /tmp 文件，再用 `--input-file /tmp/task.md`
- 不带 `--input` spawn 空壳是支持的用法（如预热、条件分发、多轮交互），但务必在流程中安排好后续 `ai send`，避免遗忘导致空跑浪费 token。

**⚠️ 推荐用 `--role`，避免手写 `--system-prompt`：** 大多数场景应使用 `--role coder`（默认值），`ai` 会自动加载对应的 system prompt。仅在需要高度定制化的 role（如 validator with specific checklist）时才用 `--system-prompt`。注意：同时设置两者时，`--system-prompt` 会覆盖 `--role`。
**🩹 防泄漏纪律（2026-09-26 实测修正）：** `ai serve` **无论是否加 `--max-turns` 都不会自动退出**——实测 `--max-turns 1` 的 agent turn 完成 45s 后进程仍存活（且仍能接受 `ai send`）。`--max-turns` 只约束对话轮数，不约束进程生命周期。因此：**cleanup 没有捷径，唯一可靠方式是显式 kill**（见下方 Cleanup recipe）；若用方式 B，session 命名必须守 `agent-$RUN_ID-*` 约定，否则 tmux 清理的 grep 匹配不到。泄漏实测案例（2026-09-26 tinyactor PGE run）：~20 个 tmux session + 29 个 `ai serve` 进程，全部源于漏 kill + 命名违约。

```bash
# 从 runtime_state 获取自己的 run ID
RUN_ID=<your run_id from runtime_state>
ID_FILE="/tmp/agent-$RUN_ID-my-task.id"
rm -f "$ID_FILE"

# bash 工具调用（参数里带 timeout: 0 —— 见上方警告）
ai serve --role coder \
  --input 'Fix the bug in auth.go' \
  --name 'fix-auth' \
  --id-file "$ID_FILE"
# → 5s 后返回 "⏳ ... detached as background operation op-N"（预期行为，不是失败）

# 等待 id 文件生成（serve 启动后写入）。sleep 必须 <5s，否则自己也被 detach
sleep 3
CHILD_ID=$(cat "$ID_FILE")

# ⚠️ 重要：记录子 agent ID 到自己的 subagent 文件（用于隔离和 cleanup）
echo "$CHILD_ID" >> ~/.ai/runs/$RUN_ID/subagent
```

**方式 B（可选）：tmux。** 仅在下列场景用：① 子 agent 输出要留档——bash 后台 op 结束时，该命令的**整段 stdout 会作为 `Background operation op-N completed` 消息灌进主 agent 上下文**，tmux pane 不会；② 父 agent 进程崩溃/重启后仍可 attach 观察；③ 需要人中途看屏幕。把上面的 `ai serve ...` 换成：

```bash
TMUX_SESSION="agent-$RUN_ID-my-task"
tmux kill-session -t "$TMUX_SESSION" 2>/dev/null   # 清理同名旧 session
tmux new-session -d -s "$TMUX_SESSION" \
  "ai serve --role coder --input-file /tmp/task.md --name 'fix-auth' --id-file $ID_FILE"
sleep 3; CHILD_ID=$(cat "$ID_FILE")
echo "$CHILD_ID" >> ~/.ai/runs/$RUN_ID/subagent
```

**如果任务描述很长**（比如超过 shell ARG_MAX），用 `--input-file`：
```bash
ID_FILE="/tmp/agent-$RUN_ID-long-task.id"
rm -f "$ID_FILE"
echo "$LONG_TASK_PROMPT" > /tmp/task-prompt.txt
ai serve --role coder --input-file /tmp/task-prompt.txt \
  --name 'long-task' --id-file "$ID_FILE"        # 同样带 timeout: 0
sleep 3; CHILD_ID=$(cat "$ID_FILE")
echo "$CHILD_ID" >> ~/.ai/runs/$RUN_ID/subagent
```

**仅在使用 `--system-prompt` 传入 @file 时支持引用 skill 目录下的文件：**

```bash
ID_FILE="/tmp/agent-$RUN_ID-validator.id"
rm -f "$ID_FILE"
ai serve --system-prompt '@$HOME/.ai/skills/debate/references/proposer-system.md' \
  --input 'Argue FOR: ...' --name 'proposer' --id-file "$ID_FILE"
sleep 3; CHILD_ID=$(cat "$ID_FILE")
echo "$CHILD_ID" >> ~/.ai/runs/$RUN_ID/subagent
```

**为什么用 `--id-file`：** 它是唯一可靠的 id 来源——`op-N` handle 不含 id，`tmux capture-pane` 依赖 pane 输出格式（不稳定）。

**为什么要 `echo >> subagent`：** 这是子 agent 隔离的关键。写入 `~/.ai/runs/$RUN_ID/subagent` 文件后：
- Cleanup 时你知道该 kill 哪些子 agent
- 你**绝对不会误杀其他 agent**（因为只 kill 文件里记录的 ID）

**⚠️ tmux session 命名规则（仅方式 B）：** 必须包含 `$RUN_ID` 前缀（如 `agent-$RUN_ID-xxx`），避免与其他 agent 的 tmux session 冲突。

### 阶段 2: 等待子 agent 完成

#### Watch Loop（标准等待方式）

`watch --follow --pretty` 是等待子 agent 的标准方式。它纯观察，不注入 prompt，不打断子 agent。

**用退出码判断结果，不要靠 footer 文本：**

| 退出码 | 含义 | 下一步 |
|--------|------|--------|
| `0` | 该轮已结束（live `_turn_end`，或 attach 时已结束走 replay） | cleanup |
| `2` | watch 自身超时，agent 可能仍在跑 | `git diff` 看产出，再 watch 一次 |
| `1` | 连不上 / 流断开 / 进程已退 | `ps -p <pid>` 确认死因 |

```bash
# ✅ 推荐：--timeout 0 = 真正无上限，等到 turn 结束
ai watch --id "$CHILD_ID" --follow --pretty --timeout 0 2>&1 | tail -20; echo "WATCH_EXIT=$?"
```

**⚠️ `--timeout` 默认是 10m，不是无限**（`defaultFollowWatchTimeout`，见 `subcommand/run/watch_model.go`）。长任务必须显式 `--timeout 0`，否则 10 分钟时以退出码 2 返回。

**⚠️ 别再等 `ai: turn complete` 这一行。** 它只在"你 attach 时该轮还活着、由 live `_turn_end` 触发"时打到 stderr；若 attach 时该轮已结束（常见：spawn → `sleep 3` → 短任务已答完），watch 走 session-replay 直接退出，**没有这一行**，stderr 只剩 `__seq:N`。两种情况退出码都是 `0`（2026-11-05 实测）。

**`--timeout` 取值：**

| 取值 | 行为 | 适用场景 |
|------|------|---------|
| `--timeout 0` | 无上限，`_turn_end` 即退出 | **绝大多数场景** |
| 不传 | 默认 10m 上限（超时退出码 2） | 短任务可以，长任务会被截断 |
| `--timeout 15m` | 显式硬上限 | CI 卡死兜底、调试 |

> `ai serve` 是长驻进程，不存在"等到进程退出"的用法；watch 只等当前这一轮。

**关键认知：**
- 一次 watch 就能等到 turn 完成，不需要循环重试
- watch 退出码 2（超时）≠ 子 agent 失败。子 agent 在后台继续运行。
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
# spawn 时在任务书里指示写文件（任务书用 --input-file 传）
echo '...Write your complete output to /tmp/result.md...Output DONE when complete.' > /tmp/task.md
ai serve --role coder --input-file /tmp/task.md --name 'long-output' --id-file "$ID_FILE"

# watch 观察（--timeout 0 = 等到 turn 结束）
ai watch --id "$CHILD_ID" --follow --pretty --timeout 0

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

# ⚠️ ai kill 按 run 记录找进程；run 已标 failed / 漏登记时进程可能还在，
# 用命令行特征兜底。绝不用 ai kill --all 或 pkill -f "ai serve"，那会杀掉其他 agent 的进程。
pkill -f "id-file /tmp/agent-$RUN_ID-" 2>/dev/null

# 验证：必须确认进程归零（ai ls 归零、tmux session 归零都不算）
pgrep -af "id-file /tmp/agent-$RUN_ID-" || echo "no ai serve processes left for $RUN_ID"

# 用了方式 B（tmux）才需要：只 kill 名字匹配 "agent-$RUN_ID-*" 的 session
tmux list-sessions -F '#{session_name}' 2>/dev/null | grep "^agent-$RUN_ID-" | while read -r s; do
  tmux kill-session -t "$s" 2>/dev/null
done

# 清理 id 文件
rm -f /tmp/agent-$RUN_ID-*.id

# 清理 subagent 记录
rm -f ~/.ai/runs/$RUN_ID/subagent
```

**为什么会泄漏（真实根因，2026-09-26 实测）：**
1. `ai serve` 是长驻服务，**任何 flag 都不会让它自动退出**（`--max-turns` 也不行，实测 45s 仍存活）——退出只能由调用方显式 kill，没有例外。
2. （仅方式 B）session 命名偏离 `agent-$RUN_ID-*` 约定（如自定义 `pge5-gen`、`b7t3-eval` 等前缀）会让 tmux 清理 `grep "^agent-$RUN_ID-"` 匹配不到任何东西，整批泄漏。
3. spawn 时**没传 `timeout: 0`**：120s 后包装进程被杀，serve 因 `setpgid` 存活成孤儿，而完成通知看起来像"已被 kill"，于是没人再去清（2026-11-05 实测）。
### 生命周期铁律

| 规则 | 违反后果 |
|------|---------|
| **每个 spawn 必须对应一个 kill**（无任何 flag 可替代） | 进程堆积，内存泄漏（每个 ~20MB） |
| **任务确认完成后立即 kill** | 延迟 kill = 忘记 kill。watch 超时不等于完成——先检查再决定 |
| **异常路径也要 kill** | 主 agent 崩溃会留下孤儿进程 |
| **spawn 后必须写入 subagent 文件** | 无法追踪子 agent，可能忘记 cleanup |
| **spawn `ai serve` 必须带 `timeout: 0`** | 120s 后包装进程被杀，serve 变孤儿且状态谎报 |
| **（用 tmux 时）tmux session 名必须含 `$RUN_ID`** | 与其他 agent 的 session 冲突 |

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

### tmux kill-session 安全模式（仅方式 B）

只能 kill **session 名匹配 `agent-$RUN_ID-*` 的**：

```bash
# ✅ 安全：按命名前缀过滤
tmux list-sessions -F '#{session_name}' 2>/dev/null | grep "^agent-$RUN_ID-" | while read -r s; do
  tmux kill-session -t "$s" 2>/dev/null
done
```

## Timeout & Watch Loop 策略

**核心原则：用默认 watch 模式等待 turn 完成，不预估任务时长。**

### `ai serve` 没有 `--timeout`（已移除）

serve 是常驻进程，跨多个 prompt 存活。一个从**进程启动**起算的墙钟 deadline 会砍掉「当时正在跑的那一轮」，而且不按 prompt 重置——第二个任务只能分到第一个任务用剩的时间，到点静默消失（`[serve] received signal: terminated`）。

所以超时职责归位到两侧：

| 想做什么 | 用什么 |
|---|---|
| 限制自己等多久 | `ai watch --timeout`（观察者侧，agent 不受影响，退出码 2） |
| 结束子 agent | `ai kill --id <id>` |
| 一次性任务要总预算 | `ai run --timeout`（由 `ai run` 在父进程侧兜底） |

### `ai watch --timeout`

**加 `--timeout 0`（无上限，`_turn_end` 即退）。不传时默认只有 10m**（`defaultFollowWatchTimeout`），长任务会被截断成退出码 2：

```bash
# ✅ 推荐：无上限，等到 turn 结束
ai watch --id "$CHILD_ID" --follow --pretty --timeout 0 2>&1 | tail -5

# ✅ 需要硬上限时（如 CI 卡死兜底）
ai watch --id "$CHILD_ID" --follow --pretty --timeout 15m 2>&1 | tail -5

# ❌ 避免：5m 硬超时导致频繁重试循环
ai watch --id "$CHILD_ID" --follow --pretty --timeout 5m
```

**⚠️ 禁止短轮询。** 每次 watch 的 toolResult 都进 context，是 compaction 次数暴涨和 token 浪费的主因。

也不要靠 `sleep` 轮询：**`sleep` 超过 5s 自己就被 bash 工具 detach**，拿不到即时结果；等待交给 watch，检查用 `cat` / `ps -p` / `ai ls` 这类瞬时命令。

### 卡死判断

watch（`--timeout 0`）会一直等到 turn 结束，所以卡死判断主要用于 watch 超时（退出码 2）返回后：

`git diff` 无变化 **且** `ai ls` 显示进程已消失 → 确认卡死/崩溃，kill 并报告。注意 rate limit retry 期间也会无输出，但不是卡死——watch 中会显示 `llm_retry` 事件。

### 超时恢复（仅 `--timeout` 模式适用）

watch 超时后：
1. ❌ 不要立即 kill
2. ✅ `git diff --stat` 检查子 agent 是否已产出文件
3. ✅ 有产出 → 子 agent 在推进 → 再 watch 一轮（`--timeout 0`）
4. ✅ 无产出且无输出 → 可能卡死 → kill
5. ✅ kill 后如果发现有产出 → 在此基础上继续，**不要从零重做**

### agent 崩溃检测

如果 watch 输出显示 `agent process <id> exited without completing turn (crash or kill)`，说明 **agent 进程已退出但未正常完成 turn**（退出码 1）：
1. 检查是否为自己管理的 agent；结合 `ai ls` 状态确认进程已退出
2. 检查 `git diff` 看有无部分产出
3. 有产出 → 在此基础上继续；无产出 → 报告给用户

如果输出显示 `agent stream closed but process <id> still alive (connection lost, not a crash)`，这是连接断开而非确认崩溃：重新执行 watch 观察，不要据此 kill agent。


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
3. 如用了 tmux，清理对应 sessions（按 `agent-$RUN_ID-*` 前缀）
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

> ⚠️ `--wait` 同样受 bash 工具 5s 自动后台影响：子 agent 超过 5s 才回复时，该 bash 调用会 detach 成 `op-N`，回复内容作为后续消息到达，而不是在当下 toolResult 里。

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

### 发送指令必须确认送达

`ai send` 后**不要只看发送端输出**，必须从对方确认已收到（`--wait` 的回复、对方 stream 中的确认、或 `watch` 对方 session 的输出）。发送端"看起来成功"≠ 对方已收到——错误可能被输出截断掩盖（见 Common Pitfalls）。

## Common Pitfalls

| ❌ Wrong | ✅ Right |
|----------|----------|
| `ai serve ... &` (用 bash `&` 后台) | ⛔ **仍禁止 `&`**：子进程继承 pipe，输出会丢；直接前台跑即可（5s 自动后台） |
| spawn `ai serve` 不带 `timeout: 0` | ⛔ 默认 120s 只杀包装 shell，serve 因 `setpgid` 存活成孤儿，完成通知谎报 killed |
| 以为 `ai watch --follow` 会阻塞住 turn | 它自己也会被 5s 规则 detach，结果作为后续消息回来 |
| 用 `sleep 5`+ 轮询子 agent 状态 | 自己也被 detach，拿不到即时结果；检查用 `ps -p` / `cat` / `ai ls` |
| `ai serve ... \| head -1` (pipe breaks serve) | `--id-file` captures ID without pipe |
| watch 后不 kill | watch 返回后 kill subagent 文件中的所有 ID |
| spawn 空壳不带 `--input` | 推荐带 `--input`；如不带，务必安排后续 `ai send`，避免遗忘空跑 |
| 用 `ai ls` status 判断完成 | `idle` 只表示"上一轮已结束"（由 events.jsonl 的 agent_end 推出），进程死后也可能残留状态；用 watch 退出码 + `ps` 判断 |
| `ai send` + `ai watch` 两步操作 | `ai send --wait` 一步完成发送+等待回复 |
| `ai send --wait` 不带消息参数 | ⛔ **必须带消息**，否则报错 `no message provided`。写 `"请给出结果"` 即可 |
| `ai send --id X --message '...'` | ⛔ **不存在 `--message` flag**；消息是位置参数：`ai send --id X "msg"`（报错会显示 `flag provided but not defined: -message`） |
| 用 `\| tail -1` / `\| head -1` 截断 send/kill/push 等**状态变更命令**的输出 | ⛔ **绝对禁止**。截断会掩盖 stderr 错误（如 flag 拼错），只剩一行无害的 usage 尾行，造成"假成功"。必须看完整错误或退出码：`cmd 2>&1; echo "EXIT=$?"` |
| kill 子 agent 后自己做它的活 | 用 `ai watch --follow` 观察进度，让子 agent 自己完成 |
| `tmux kill-server` 清理环境 | ⛔ **绝对禁止**，会杀掉所有 tmux session |
| `ai ls` 看到就 kill | ⛔ **绝对禁止**，只 kill subagent 文件中记录的 ID |
| （用 tmux 时）tmux session 名不含 `$RUN_ID` | 必须用 `agent-$RUN_ID-xxx` 格式，避免冲突 |
| 忘记写入 subagent 文件 | spawn 后必须 `echo "$CHILD_ID" >> ~/.ai/runs/$RUN_ID/subagent` |
| 用 `ai kill` 杀不属于自己的 agent | ⛔ 只 kill 自己 subagent 文件里的 ID |
| 长输出任务用 `send --wait --summary` 收集 | ⛔ `--summary` 截断长输出；长任务用写文件 + watch |
| 用 `ai send` 查询正在干活的子 agent 进度 | ⛔ `send` 会注入新 prompt **打断**子 agent；用 `watch --follow --pretty` 观察 |
| `ai watch` 不加 `--pretty` | ⛔ 非 TTY 环境（脚本/管道）不加 `--pretty` 会无输出 |
| 用 `ai watch`（不带 `--follow`）当历史回放 | ⛔ 实测 0 行后阻塞；读已完成 turn 用 session transcript / `ai history` |
| watch 超时后立即 kill | ⛔ 超时 ≠ 失败；先 `git diff` 检查产出，有变化就再 watch 一轮 |
| `ai watch --timeout 5m` 反复重试 | ⚠️ 用 `--timeout 0` 一次等到 turn 结束；不传 `--timeout` 的默认值是 10m |
| `--input` 内联多行长文本 | ⚠️ `/bin/sh -c` 解析 + 多行转义会让 serve 立即退出。长文本（>100 chars）必须用 `--input-file /tmp/task.md` |
| kill 前不确认子 agent 是否在推进 | 连续两轮 watch 无输出且 `git diff` 无变化才考虑 kill |
| kill 后发现有产出却从零重做 | ⛔ 先 `git diff` 检查子 agent 产出，在此基础上继续 |
| 编排多任务时 watch 死等每一个 | ⛔ 用 NOTIFY 回调模式：任务书写明完成时 `ai send --id <父RUN_ID>`，父 agent 事件驱动 |
| 死掉的 agent 用新任务书从头交代 | 用 `/resume <旧 session uuid>` 原位复活（同工作目录 serve 空壳 + send '/resume'；session uuid 取自 run.json 的 `session` 字段，见 Crash Recovery），上下文零损失 |
| 相信 `ai ls` 的 running 状态 | 进程死后 status 可能仍显示 running；用 `ps -p <pid>` + send 探针确认 |

## Event-Driven Orchestration（NOTIFY 回调模式）

**适用：** 编排多个长任务子 agent 时，父 agent 不应阻塞在 watch 上。让子 agent 完成时**主动唤醒**父 agent：

1. 任务 prompt 中写入 NOTIFY 规则：`完成/失败/GATE 时执行 ai send --id <父RUN_ID> 'NOTIFY <任务名> <状态> <结果文件路径>'`
2. 父 agent 工具调用协议停下也无妨——`ai send` 会作为新消息唤醒它
3. 父 agent 醒来只做：读结果文件首行 STATUS → 记账 → 派下一棒

```bash
# 任务书模板（写入 /tmp/task.md 后 --input-file 传入）
# 完成时必须：ai send --id <父RUN_ID> 'NOTIFY issue-123 MERGED /tmp/result.md'
```

**配套约定：**
- 结果文件首行必须是机器可读的 `STATUS: <STATE>`（SUCCESS/GATE-BLOCKED/MERGED:<sha>/REVIEW_STALLED...）
- 并发上限见上文 Concurrency Limit（限流事故后应进一步收紧）；子 agent 自派的孙 agent 也必须追加登记到 `~/.ai/runs/$RUN_ID/subagent`（kill 安全依赖此文件，任务书中显式要求）
- 重负载任务书写明资源纪律：单次单个构建/测试命令、只跑 scoped 测试、外部集群用完即拆（并行 worktree 各自构建极易挤爆内存/触发 OOM 连环杀死 serve 进程）

## Crash Recovery（/resume 原位复活）

子 agent 进程死了（OOM/限流/宿主重启）但 session 数据还在时，**不必重新交代任务书**：

```bash
# 1. 先确保工作目录与原 agent 一致（可用 change_workspace，或 `cd <dir> && ai serve ...`）
#    再起一个新 serve 空壳（bash 调用带 timeout: 0）
ai serve --name issue-XXr --id-file /tmp/agent-$RUN_ID-issue-XXr.id
sleep 3; NEW_ID=$(cat /tmp/agent-$RUN_ID-issue-XXr.id)
echo "$NEW_ID" >> ~/.ai/runs/$RUN_ID/subagent   # 新 id 也要登记

# 2. run id ≠ session id：run.json 的 session 字段才是真正的 session uuid
#    （serve 创建/切换 session 时写入；无该字段 = 早于该字段的旧 run）
SESS_ID=$(jq -r '.session' ~/.ai/runs/<旧RUN_ID>/run.json)   # 也可直接 cat/read 该文件
ai send --id "$NEW_ID" "/resume $SESS_ID"

# 3. 可选：补发一条纪律/上下文更新消息
ai send --id "$NEW_ID" '恢复继续，注意 ...'
```

要求：新 serve 的工作目录必须与原 agent 一致。恢复后子 agent 带全部历史上下文继续，零重复 briefing。

## Health Check（真实探活三件套）

**`ai ls` 的 status 会撒谎**：进程被 OOM 杀死后仍可能显示 `running`。怀疑群体死亡时按序检查：

```bash
# 1. ps 里找 serve 进程（按 --name 或 --input-file 特征过滤）
ps aux | grep 'ai serve' | grep -v grep

# 2. 进程按 id-file 特征扫（方式 A 的可靠信号；用 tmux 时也可查 pane_dead）
#    ⚠️ pgrep -f 会自我匹配包装 shell，判断死活用 `ps -p <pid>` 或 bracket 模式 [a]i serve
pgrep -af "id-file /tmp/agent-$RUN_ID-"

# 3. send 探针：报 "run X is not running" 即死（这条例外允许 send——探活优先于打断风险；
#    但不得截断输出，否则会掩盖真实错误，退出码也被 pipe 吞掉）
ai send --id "$CHILD_ID" 'STATUS CHECK: 一行回报当前阶段' 2>&1; echo "EXIT=$?"
```

**死亡现场的抢救顺序：** 先清点 worktree/结果文件（工作通常都在，零损失）→ 按 /resume 原位复活或用「现场文件+新任务书」接盘 → 复活消息中附带资源纪律。

## Relationship to Other Skills

本技能是子 agent 操作的**单一事实来源**。其他技能引用本技能即可，无需重复定义 spawn/watch/kill 流程。

| Skill | Uses subagent for |
|-------|-------------------|
| `pge` | Generator and Validator agents |
| `explore` | Parallel codebase exploration |
| `debate` | Proposer and opposer agents |
| `review` | Reviewer agent |
| `worker-judge` | Worker and judge agents |

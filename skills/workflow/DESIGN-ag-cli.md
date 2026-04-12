# Agent Orchestration CLI Design (`ag`)

> goroutine + channel 的 agent 等价物。
> 每个命令做一件事，可组合，可编程。

## 核心抽象

三个实体：

```
Agent    — 一个运行中的 LLM agent 进程（有 ID、有 inbox、有生命周期）
Channel  — 一个命名消息队列（agent 之间解耦通信）
Task     — 一个可认领的工作单元（有状态、有产出）
```

所有状态存于 `.ag/` 目录，纯文件系统，零依赖。

```
.ag/
├── agents/
│   └── worker-1/
│       ├── meta.json        # { prompt, mode, pid, ... }
│       ├── status           # spawning | running | done | failed
│       ├── exit-code        # 0 | 1 | ...
│       ├── inbox/           # FIFO message queue
│       │   ├── 001.msg
│       │   └── 002.msg
│       └── output           # agent 最终产出
├── channels/
│   └── review-queue/
│       ├── 001.msg
│       └── 002.msg
└── tasks/
    ├── t001.json
    └── t002.json
```

---

## 命令设计

### 1. Agent 生命周期

#### `ag spawn` — 启动一个 agent

```bash
ag spawn [OPTIONS]

Options:
  --id <name>           Agent 唯一标识（必需）
  --system <file|text>  System prompt / 角色定义
  --input <file|text>   初始输入（任务描述）
  --mode <mode>         headless（默认）| rpc
  --cwd <dir>           工作目录（默认当前目录）
  --timeout <seconds>   超时时间（默认 0 = 无限）

# headless: 跑完即退出，产出写入 output
ag spawn --id worker-1 --system reviewer.md --input task.md

# rpc: 持续运行，通过 send/recv 交互
ag spawn --id worker-1 --system worker.md --mode rpc

# 非阻塞，立即返回
# 产出: Agent spawned: worker-1 (pid 12345)
```

#### `ag wait` — 等待 agent 完成

```bash
ag wait <id> [--timeout <seconds>]

# 阻塞直到 agent 状态变为 done 或 failed
# 返回 exit code: 0 = 成功, 1 = 失败, 2 = 超时

ag wait worker-1
ag wait worker-1 --timeout 300
```

#### `ag kill` — 终止 agent

```bash
ag kill <id>

# 发送 SIGTERM，等待退出
# 更新 status 为 failed
```

#### `ag status` — 查看状态

```bash
ag status <id>

# 输出:
#   worker-1  running  pid:12345  uptime:2m30s  inbox:3
```

#### `ag output` — 获取最终产出

```bash
ag output <id>

# 输出 agent 的最终结果到 stdout
# agent 未完成则报错

ag output worker-1 > result.md
```

#### `ag ls` — 列出所有 agent

```bash
ag ls [--status <status>]

# 输出:
#   worker-1    running   2m30s
#   reviewer-1  done      5m12s
```

---

### 2. 通信

#### `ag send` — 发送消息

```bash
ag send <target> [content]
ag send <target> --file <file>

# target 可以是:
#   agent id  → 投递到该 agent 的 inbox
#   channel名 → 投递到该 channel

# 从 stdin
echo "fix the bug" | ag send worker-1

# 从文件
ag send worker-1 --file feedback.md

# 发到 channel
ag send review-queue --file worker-output.md
```

#### `ag recv` — 接收消息

```bash
ag recv <source> [--wait] [--timeout <seconds>] [--all]

# source 可以是:
#   agent id  → 从该 agent 的 output（headless）或 inbox（rpc，本 agent 自己的）
#   channel名 → 从该 channel 取一条

# --wait    阻塞等待直到有消息（默认非阻塞，无消息则退出 1）
# --timeout 配合 --wait，超时退出
# --all     取出所有消息（不删），默认取一条并移除

# 非阻塞检查
ag recv review-queue || echo "no messages"

# 阻塞等待 worker 产出（headless 模式下等同 ag output）
ag recv worker-1 --wait

# rpc 模式的 agent 读自己的 inbox
ag recv self --wait --timeout 60
```

> **关于 `self`**: 当 agent 在 rpc 模式下运行时，`ag recv self` 读取自己的 inbox。
> 这让 rpc agent 能进入 "recv → 处理 → send → recv" 的事件循环。

---

### 3. Channel 管理

#### `ag channel create` — 创建 channel

```bash
ag channel create <name>
```

#### `ag channel ls` — 列出 channels

```bash
ag channel ls

# 输出:
#   review-queue    3 messages
#   results         0 messages
```

#### `ag channel rm` — 删除 channel

```bash
ag channel rm <name>
```

> `send` 和 `recv` 已经统一处理 agent inbox 和 channel。
> channel 子命令只负责生命周期管理。

---

### 4. Task

#### `ag task create` — 创建任务

```bash
ag task create <description> [--file <spec-file>]

# 输出: t001
# 创建一个 status=pending 的任务
```

#### `ag task list` — 列出任务

```bash
ag task list [--status pending|claimed|done|failed]

# 输出:
#   t001  claimed   worker-1   "Fix auth bug"
#   t002  pending   -          "Add unit tests"
#   t003  done      worker-2   "Update README"
```

#### `ag task claim` — 认领任务

```bash
ag task claim <task-id> [--as <agent-id>]

# 原子操作：pending → claimed
# 已被认领则失败（exit 1）
# --as 缺省则用 $AG_AGENT_ID 环境变量
```

#### `ag task done` — 完成任务

```bash
ag task done <task-id> [--output <file>]

# claimed → done
# 附带产出
```

#### `ag task fail` — 标记失败

```bash
ag task fail <task-id> [--error <message>]

# claimed → failed
```

#### `ag task show` — 查看任务详情

```bash
ag task show <task-id>

# 输出:
#   id: t001
#   status: done
#   claimant: worker-1
#   description: Fix auth bug
#   output: .ag/tasks/t001/output
```

---

## 完整命令速查

```
Agent:     spawn | wait | kill | status | output | ls
Message:   send | recv
Channel:   channel create | channel ls | channel rm
Task:      task create | task list | task claim | task done | task fail | task show
```

---

## 组合示例

### Pattern 1: Worker-Judge Loop

```bash
#!/bin/bash
# worker-judge.sh — 直到 judge 认可才退出

ag spawn --id worker --system worker.md --mode rpc
ag spawn --id judge  --system judge.md  --mode rpc

ag send worker --file task.md

MAX_ROUNDS=5
for i in $(seq 1 $MAX_ROUNDS); do
  # 等 worker 产出
  result=$(ag recv worker --wait --timeout 600)
  
  # 交给 judge
  ag send judge --file <(echo "$result")
  verdict=$(ag recv judge --wait --timeout 120)
  
  if echo "$verdict" | grep -q "APPROVED"; then
    echo "$result"
    ag kill worker
    ag kill judge
    exit 0
  fi
  
  # 反馈给 worker
  ag send worker --file <(echo "$verdict")
done

ag kill worker
ag kill judge
echo "Max rounds reached"
exit 1
```

### Pattern 2: 并行探索 + 汇总

```bash
#!/bin/bash
# parallel-explore.sh — N 个 explorer 并行，1 个汇总

ag channel create explore-results

# 并行 spawn
for dir in src/api src/core src/web; do
  ag spawn --id "explore-$dir" --system explorer.md --input "$dir" &
done
wait

# 收集结果到 channel
for dir in src/api src/core src/web; do
  ag output "explore-$dir" | ag send explore-results
done

# 汇总
all_results=$(ag recv explore-results --all)
ag spawn --id summarizer --system summarizer.md --input <(echo "$all_results")
ag wait summarizer
ag output summarizer
```

### Pattern 3: Pipeline（阶段串联）

```bash
#!/bin/bash
# pipeline.sh — 串行阶段，每阶段有 checkpoint

stages=(spec plan implement test)

prev_output="task.md"
for stage in "${stages[@]}"; do
  ag spawn --id "$stage" --system "${stage}-worker.md" --input <(cat "$prev_output")
  ag wait "$stage"
  
  if [ $? -ne 0 ]; then
    echo "Stage $stage failed"
    exit 1
  fi
  
  prev_output=$(mktemp)
  ag output "$stage" > "$prev_output"
done
```

### Pattern 4: Work Stealing（任务队列）

```bash
#!/bin/bash
# work-steal.sh — N 个 worker 从任务队列认领

# 创建任务
for bug in $(cat bugs.txt); do
  ag task create "$bug"
done

# 启动 worker pool
for i in $(seq 1 3); do
  ag spawn --id "worker-$i" --system worker.md --mode rpc &
done

# 每个 worker 内部循环（在 worker.md prompt 中引导 agent 执行）:
#   while task=$(ag task list --status pending | head -1); do
#     ag task claim $task --as worker-$i
#     ... do work ...
#     ag task done $task --output result.md
#   done
```

### Pattern 5: Fan-out / Fan-in

```bash
#!/bin/bash
# fan-out-fan-in.sh — 一个任务拆成 N 份，并行执行，合并结果

ag channel create partial-results

# 拆分（由 agent 做）
ag spawn --id planner --system planner.md --input task.md
ag wait planner
subtasks=$(ag output planner)

# 分发
i=0
echo "$subtasks" | while read -r subtask; do
  ag spawn --id "worker-$i" --system worker.md --input <(echo "$subtask")
  i=$((i + 1))
done

# 等待所有 worker 完成，收集到 channel
for j in $(seq 0 $((i-1))); do
  ag wait "worker-$j"
  ag output "worker-$j" | ag send partial-results
done

# 合并
ag spawn --id merger --system merger.md --input <(ag recv partial-results --all)
ag wait merger
ag output merger
```

---

## 设计原则

1. **文件即状态** — 所有状态在 `.ag/` 下，可读可调试可 gitignore
2. **原子操作** — send 用 `mktemp + mv`，claim 用 `ln`，无竞争
3. **阻塞是可选的** — `--wait` 让你选 poll 还是 block
4. **agent 无感知** — agent 不需要知道编排层的存在，它只看到 stdin/stdout + 可选的 `ag` CLI
5. **headless 和 rpc 统一** — 同一套命令，headless 下由编排者驱动，rpc 下 agent 自己驱动
6. **可编程** — 所有命令可在 bash 中组合，不需要 YAML/JSON 配置

---

## 实现优先级

```
Phase 1 — 能跑（MVP）
  ag spawn (headless only)
  ag wait
  ag output
  ag kill
  ag ls / status

Phase 2 — 能通信
  ag send / recv
  ag channel create / ls / rm

Phase 3 — 能协作
  ag task create / claim / done / fail / list / show

Phase 4 — rpc 模式
  ag spawn --mode rpc
  ag recv self --wait
```
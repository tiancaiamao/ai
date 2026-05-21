---
name: explore
description: Explore codebases, repositories, or topics and collect key information for later phases. Use for code exploration, architecture analysis, and information gathering before implementation.
---

# Explore Skill

使用 `ai serve` 派生 **独立子 agent** 探索代码库、仓库或主题，收集关键信息供后续阶段使用。

## ⚠️ MANDATORY: 必须使用子 agent 执行

**当用户触发 explore 技能时，你（调度 agent）必须通过 `ai serve` + tmux 派生子 agent 执行探索。禁止你自己直接用 bash/read/grep 探索。**

原因：
- 子 agent 作为独立进程运行，不阻塞主对话
- 每个 agent 有独立的上下文窗口（context firewall），不污染主 agent 的对话历史
- 可以并行派生多个子 agent 同时探索不同目标
- explorer.md persona 指导子 agent 按标准格式输出

**子 agent 操作详见 `subagent` 技能。**

## ⛔ CONCURRENCY LIMIT: 最多 2 个子 agent 同时运行

**主 agent + 子 agent 同时运行总数不得超过 3**（即最多 2 个子 agent）。

原因：LLM 提供商在并发稍高时即触发 rate limit，导致子 agent 卡住或失败。

**规则**：
- 单次 spawn 上限：2 个子 agent
- 需要探索 3+ 个目标时，必须分批：先 spawn 2 个 → wait → cleanup → 再 spawn 下一批

**你的角色（调度 agent）**：解析用户意图 → 构造 input → 派生子 agent → 等待 → 汇总结果
**子 agent 角色（explorer）**：按 `explorer.md` persona 执行实际探索，写入文件

## 执行流程（强制）

### Step 1: 准备

```bash
# 被探索项目的路径
TARGET_PROJECT="/path/to/project"

# 在目标项目中创建输出目录
mkdir -p "$TARGET_PROJECT/explorer"
```

### Step 2: 派生子 agent

⚠️ **`ai serve` 是阻塞命令，必须用 tmux 后台运行。**

```bash
# 单目标探索
TARGET_PROJECT="/path/to/project"

tmux new-session -d -s "explore-auth" \
  "ai serve --system-prompt '@$HOME/.ai/skills/explore/explorer.md' \
   --input 'Explore the authentication module. Focus on: how auth works, middleware chain, token handling. Write findings to: $TARGET_PROJECT/explorer/auth.md' \
   --name 'explore-auth' \
   --timeout 15m"

sleep 2
EXPLORE_ID=$(tmux capture-pane -t "explore-auth" -p | head -1 | tr -d '[:space:]')
```

```bash
# 多目标并行探索（最多 2 个 agent 同时）
TARGET_PROJECT="/path/to/project"

# Agent 1
tmux new-session -d -s "explore-auth" \
  "ai serve --system-prompt '@$HOME/.ai/skills/explore/explorer.md' \
   --input 'Explore the authentication module. Write findings to: $TARGET_PROJECT/explorer/auth.md' \
   --name 'explore-auth' \
   --timeout 15m"

# Agent 2
tmux new-session -d -s "explore-rpc" \
  "ai serve --system-prompt '@$HOME/.ai/skills/explore/explorer.md' \
   --input 'Explore the RPC handling layer. Write findings to: $TARGET_PROJECT/explorer/rpc.md' \
   --name 'explore-rpc' \
   --timeout 15m"

sleep 2
ID_AUTH=$(tmux capture-pane -t "explore-auth" -p | head -1 | tr -d '[:space:]')
ID_RPC=$(tmux capture-pane -t "explore-rpc" -p | head -1 | tr -d '[:space:]')
```

### Step 3: 等待并收集

```bash
# Watch agent output (blocks until agent finishes or timeout)
ai watch --id "$ID_AUTH" --follow --pretty

# If parallel, wait for the second one too
ai watch --id "$ID_RPC" --follow --pretty

# Results are written by the explorer agent to $TARGET_PROJECT/explorer/<target>.md
```

### Step 4: 清理

```bash
ai kill --id "$ID_AUTH" 2>/dev/null
ai kill --id "$ID_RPC" 2>/dev/null
tmux kill-session -t "explore-auth" 2>/dev/null
tmux kill-session -t "explore-rpc" 2>/dev/null
```

### Step 5: 汇总

读取 `$TARGET_PROJECT/explorer/` 下的文件，向用户展示关键发现。

## 输入构造指南

给子 agent 的 `--input` 应包含：

1. **探索目标**：要探索什么（代码库路径、模块名、主题）
2. **关注点**：用户特别想了解的方面（可选）
3. **输出路径**：`Write findings to: <绝对路径>`（必须使用绝对路径）

示例 input：
```
Explore the project at /Users/me/project/myapp. Focus on:
- Overall architecture and module boundaries
- How authentication is implemented
- Database layer and ORM usage
Write findings to: /Users/me/project/myapp/explorer/architecture.md
```

## 特殊场景

### 架构约束探索

当用户提到架构变更或重构时，input 中追加：

```
This is an architecture exploration. You MUST also include:
- Layer/package boundaries and dependency directions
- Existing patterns for similar functionality
- Integration points: who uses this code and how
Output an "Architecture Constraints" section with a checklist.
```

### 大型仓库

对大型仓库，按模块拆分为多批探索（每批最多 2 个 agent）：

```bash
TARGET_PROJECT="/path/to/large/repo"

# === Batch 1: spawn up to 2 agents ===
tmux new-session -d -s "explore-auth" \
  "ai serve --system-prompt '@$HOME/.ai/skills/explore/explorer.md' \
   --input 'Explore the auth module under src/auth/. Write findings to: $TARGET_PROJECT/explorer/auth.md' \
   --name 'explore-auth' --timeout 15m"

tmux new-session -d -s "explore-api" \
  "ai serve --system-prompt '@$HOME/.ai/skills/explore/explorer.md' \
   --input 'Explore the API layer under src/api/. Write findings to: $TARGET_PROJECT/explorer/api.md' \
   --name 'explore-api' --timeout 15m"

sleep 2
ID1=$(tmux capture-pane -t "explore-auth" -p | head -1 | tr -d '[:space:]')
ID2=$(tmux capture-pane -t "explore-api" -p | head -1 | tr -d '[:space:]')

# Wait for batch 1
ai watch --id "$ID1" --follow --pretty
ai watch --id "$ID2" --follow --pretty

# Cleanup batch 1
ai kill --id "$ID1" 2>/dev/null; ai kill --id "$ID2" 2>/dev/null
tmux kill-session -t "explore-auth" 2>/dev/null; tmux kill-session -t "explore-api" 2>/dev/null

# === Batch 2: spawn next 2 agents ===
tmux new-session -d -s "explore-storage" \
  "ai serve --system-prompt '@$HOME/.ai/skills/explore/explorer.md' \
   --input 'Explore the storage module under src/storage/. Write findings to: $TARGET_PROJECT/explorer/storage.md' \
   --name 'explore-storage' --timeout 15m"

tmux new-session -d -s "explore-infra" \
  "ai serve --system-prompt '@$HOME/.ai/skills/explore/explorer.md' \
   --input 'Explore the infrastructure layer. Write findings to: $TARGET_PROJECT/explorer/infra.md' \
   --name 'explore-infra' --timeout 15m"

# Wait and cleanup batch 2...
```

## 输出约定

- 目录：`explorer/`（项目根目录下）或用户指定路径
- 命名：`<target>.md`
- 格式：由 `explorer.md` persona 定义的标准 Markdown 结构

## 错误处理

### ⚠️ MANDATORY: 子 agent 失败时，停止并报告给用户

当子 agent 失败或超时时，**禁止自行诊断或重试**。立即向用户报告失败信息。

**报告内容**：展示 agent 的最后输出（通过 `ai watch` 已获得）

**禁止的操作**：
- ❌ 盲目重新 spawn（不做分析就重试）
- ❌ 自行修改参数后重试
- ❌ 自己直接用 bash/read/grep 探索（绕过子 agent）

**唯一允许的操作**：
- ✅ 报告失败信息给用户
- ✅ 在用户明确指示后才执行后续操作

## 流程定位

```
用户需求
    ↓
┌─────────────────────┐
│  explore 技能        │  ← 你在这里：用 ai serve 派生子 agent
└──────────┬──────────┘
           ↓
explorer/*.md
           ↓
┌─────────────────────┐
│  brainstorm 技能     │  ← 基于探索结果决策
└──────────┬──────────┘
           ↓
design.md → PGE（Orchestrator + Generator + Evaluator）
```
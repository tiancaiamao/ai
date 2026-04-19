---
name: explore
description: Explore codebases, repositories, or topics and collect key information for later phases. Use for code exploration, architecture analysis, and information gathering before implementation.
---

# Explore Skill

使用 `ag` CLI 派生 **独立子 agent** 探索代码库、仓库或主题，收集关键信息供后续阶段使用。

## ⚠️ MANDATORY: 必须使用 ag 子 agent 执行

**当用户触发 explore 技能时，你（调度 agent）必须通过 `ag` 派生子 agent 执行探索。禁止你自己直接用 bash/read/grep 探索。**

原因：
- 子 agent 在独立 tmux 会话中运行，不阻塞主对话
- 可以并行派生多个子 agent 同时探索不同目标
- explorer.md persona 指导子 agent 按标准格式输出

## ⛔ CONCURRENCY LIMIT: 最多 2 个子 agent 同时运行

**主 agent + 子 agent 同时运行总数不得超过 3**（即最多 2 个子 agent）。

原因：LLM 提供商在并发稍高时即触发 rate limit，导致子 agent 卡住或失败。

**规则**：
- 单次 spawn 上限：2 个子 agent
- 需要探索 3+ 个目标时，必须分批：先 spawn 2 个 → `wait` → `rm` → 再 spawn 下一批
- 不可用循环一次性 spawn 超过 2 个 agent

**你的角色（调度 agent）**：解析用户意图 → 构造 input → 派生 ag agent → 等待 → 汇总结果
**子 agent 角色（explorer）**：按 `explorer.md` persona 执行实际探索，写入文件

## 执行流程（强制）

### Step 1: 准备

```bash
# 被探索项目的路径
TARGET_PROJECT="/path/to/project"

# 确保 ag 已构建
AG_BIN="$HOME/.ai/skills/ag/ag"
if [ ! -x "$AG_BIN" ]; then
  (cd "$HOME/.ai/skills/ag" && go build -o ag .)
fi

# 在目标项目中创建输出目录
mkdir -p "$TARGET_PROJECT/explorer"
```

### Step 2: 派生子 agent

⚠️ **重要**：`--cwd` 必须传被探索项目的路径，否则子 agent 会在当前 shell 的 CWD 下运行，导致探索错误的项目。

```bash
# 单目标探索
TARGET_PROJECT="/path/to/project"  # 被探索的项目路径

$AG_BIN agent spawn explore-<TARGET> \
  --cwd "$TARGET_PROJECT" \
  --system @"$HOME/.ai/skills/explore/explorer.md" \
  --input "<探索指令>. Write findings to: $TARGET_PROJECT/explorer/<target>.md"
```

```bash
# 多目标并行探索（同时启动多个 agent）
TARGET_PROJECT="/path/to/project"

$AG_BIN agent spawn explore-auth \
  --cwd "$TARGET_PROJECT" \
  --system @"$HOME/.ai/skills/explore/explorer.md" \
  --input "Explore the authentication module. Write findings to: $TARGET_PROJECT/explorer/auth.md"

$AG_BIN agent spawn explore-rpc \
  --cwd "$TARGET_PROJECT" \
  --system @"$HOME/.ai/skills/explore/explorer.md" \
  --input "Explore the RPC handling layer. Write findings to: $TARGET_PROJECT/explorer/rpc.md"
```

### Step 3: 等待并收集

```bash
# 等待完成
$AG_BIN agent wait explore-<TARGET> --timeout 600

# 查看结果（输出已写入 $TARGET_PROJECT/explorer/<target>.md，由子 agent 直接写文件）
$AG_BIN agent rm explore-<TARGET>
```

### Step 4: 汇总

读取 `$TARGET_PROJECT/explorer/` 下的文件，向用户展示关键发现。

## 输入构造指南

给子 agent 的 `--input` 应包含：

1. **探索目标**：要探索什么（代码库路径、模块名、主题）
2. **关注点**：用户特别想了解的方面（可选）
3. **输出路径**：`Write findings to: <绝对路径>`（必须使用目标项目下的绝对路径）

⚠️ **输出路径必须使用目标项目的绝对路径**，不要依赖相对路径或 `$EXPLORER_DIR` 变量，因为子 agent 的 CWD 由 `--cwd` 控制。

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
$AG_BIN agent spawn explore-auth \
  --cwd "$TARGET_PROJECT" \
  --system @"$HOME/.ai/skills/explore/explorer.md" \
  --input "Explore the auth module under src/auth/. Write findings to: $TARGET_PROJECT/explorer/auth.md"

$AG_BIN agent spawn explore-api \
  --cwd "$TARGET_PROJECT" \
  --system @"$HOME/.ai/skills/explore/explorer.md" \
  --input "Explore the API layer under src/api/. Write findings to: $TARGET_PROJECT/explorer/api.md"

# Wait and clean up batch 1
$AG_BIN agent wait explore-auth explore-api --timeout 900
$AG_BIN agent rm explore-auth explore-api

# === Batch 2: spawn next 2 agents ===
$AG_BIN agent spawn explore-storage \
  --cwd "$TARGET_PROJECT" \
  --system @"$HOME/.ai/skills/explore/explorer.md" \
  --input "Explore the storage module under src/storage/. Write findings to: $TARGET_PROJECT/explorer/storage.md"

$AG_BIN agent spawn explore-infra \
  --cwd "$TARGET_PROJECT" \
  --system @"$HOME/.ai/skills/explore/explorer.md" \
  --input "Explore the infrastructure layer. Write findings to: $TARGET_PROJECT/explorer/infra.md"

# Wait and clean up batch 2
$AG_BIN agent wait explore-storage explore-infra --timeout 900
$AG_BIN agent rm explore-storage explore-infra
```

## 输出约定

- 目录：`explorer/`（项目根目录下）或用户指定路径
- 命名：`<target>.md`
- 格式：由 `explorer.md` persona 定义的标准 Markdown 结构

## 错误处理

```bash
# 检查 agent 状态
$AG_BIN agent status explore-<TARGET>

# 如果失败，查看错误
$AG_BIN agent output explore-<TARGET>

# 清理 agent 元数据（也清理 tmux 会话）
$AG_BIN agent rm explore-<TARGET>

# 如果 agent rm 后 tmux 会话仍残留，手动清理
tmux kill-session -t ag-explore-<TARGET> 2>/dev/null
```

## 流程定位

```
用户需求
    ↓
┌─────────────────────┐
│  explore 技能        │  ← 你在这里：用 ag 派生子 agent
└──────────┬──────────┘
           ↓
explorer/*.md
           ↓
┌─────────────────────┐
│  brainstorm 技能     │  ← 基于探索结果决策
└──────────┬──────────┘
           ↓
decisions.md → spec.md → plan.md → implement
```

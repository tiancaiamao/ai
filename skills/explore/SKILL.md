---
name: explore
description: Explore codebases, repositories, or topics and collect key information for later phases. Use for code exploration, architecture analysis, and information gathering before implementation.
allowed-tools: [bash, read, grep, write]
---

# Explore Skill

Explore codebases, repositories, or topics and collect key information for later phases. Similar to the `review` skill, this can run as an autonomous subagent.

## 设计理念

**来源参考**：
- pi-config 的 Scout agent：只读探索，专注信息收集
- orchestrate 的 Researcher persona：调查和收集
- review 技能的 subagent 模式：独立执行

**核心原则**：
- 探索，但不修改
- 收集关键信息，不深入细节
- 输出到独立文件，供后续阶段使用
- 可以并行探索多个目标

## 使用方式

```
/skill:explore 探索 ~/project/pi-mono 的 subagent 实现
/skill:explore 探索 https://github.com/user/repo 的架构设计
/skill:explore 并行探索 repo1 和 repo2 的 auth 模块实现
/skill:explore 探索当前项目的任务调度机制
```

## 探索阶段 vs 后续阶段

| 阶段 | 职责 | 输出 |
|------|------|------|
| **explore** | 收集信息，理解现状 | `explorer/*.md` |
| **brainstorming** | 基于收集的信息决策 | `decisions.md` |
| **/workflow start feature** | 制定计划 | artifact 目录中的 SPEC.md, PLAN.md |
| **worker** | 执行实现 | 修改的代码 |
| **review** | 验收代码 | `reviews/*.md` |

## ⚠️ Architecture Constraint Exploration

**For tasks involving architecture changes, explore MUST include:**

1. **Layer/Package boundaries**
   - Where should the code live?
   - What are the dependency directions?
   - Which packages should stay pure?

2. **Existing patterns**
   - How does similar functionality exist elsewhere?
   - What patterns should be followed?

3. **Integration points**
   - Who will use this code?
   - How will they integrate?

**Output section to add:**
```markdown
## 架构约束
- [ ] [约束 1]: 例如 "agent core 不应该依赖 RPC"
- [ ] [约束 2]: 例如 "命令处理应该在 agent 外部"
- [ ] [约束 3]: 例如 "新包应该可被多个项目复用"
```

## 使用 ag 执行 Explore

**⚠️ 重要：使用 `ag` CLI 而不是 subagent**

`subagent` skill 已经被废弃，统一使用 `ag` CLI。

### 基本用法

```bash
export AG_BIN=~/.ai/skills/ag/ag

# 短任务
$AG_BIN spawn \
  --id "explore-rpc" \
  --system @/path/to/explorer.md \
  --input "Explore RPC handling" \
  --timeout 5m

$AG_BIN wait "explore-rpc" --timeout 300
OUTPUT=$($AG_BIN output "explore-rpc")
$AG_BIN rm "explore-rpc"
```

### 长任务

```bash
# 长任务 (>5min): 增加 timeout
$AG_BIN spawn \
  --id "explore-auth" \
  --system @/path/to/explorer.md \
  --input "Explore auth module in depth" \
  --timeout 15m

$AG_BIN wait "explore-auth" --timeout 900
OUTPUT=$($AG_BIN output "explore-auth")
$AG_BIN rm "explore-auth"
```

### 输出到文件

让 explorer agent 写入文件，而不是 stdout：

```bash
# 在 input 中指定输出路径
$AG_BIN spawn \
  --id "explore-target" \
  --system @/path/to/explorer.md \
  --input "Explore ~/project/pi-mono. Write findings to: /tmp/explorer/target.md" \
  --timeout 10m

$AG_BIN wait "explore-target" --timeout 600
$AG_BIN rm "explore-target"

# 查看结果
cat /tmp/explorer/target.md
```

### 并行探索

使用 `ag patterns/parallel.sh`：

```bash
# 并行探索两个目标
$AG_BIN patterns/parallel.sh \
  2 \                       # 2 个 agents
  @/path/to/explorer.md \    # system prompt
  "Explore auth module" \      # 主题
  /tmp/explore-results        # 输出目录

# 结果: /tmp/explore-results/agent-0.md, agent-1.md
```

### 管道化（探索 → 分析）

使用 `ag patterns/pipeline.sh`：

```bash
# 先探索，再分析
$AG_BIN patterns/pipeline.sh \
  "Explore RPC handling. Output to: /tmp/rpc-findings.md" \
  @/path/to/explorer.md \
  @/path/to/analyzer.md
```

## ag vs subagent 对比

| 功能 | subagent (旧) | ag (新) |
|------|---------------|----------|
| **启动** | `start_subagent_tmux.sh` | `ag spawn --id ...` |
| **等待** | `tmux_wait.sh` | `ag wait ...` |
| **获取输出** | `cat output.txt` | `ag output ...` |
| **清理** | 手动删除 | `ag rm ...` |
| **状态检查** | 手动 `tmux ls` | `ag status/ls` |
| **并行执行** | 手动脚本 | `ag patterns/parallel.sh` |
| **Worker-Judge 循环** | 手动循环 | `ag patterns/pair.sh` |

## 迁移指南

如果你有使用 `subagent` 的代码：

**旧方式：**
```bash
SESSION=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/explore-out.txt 10m \
  @explorer.md \
  "Explore code")

SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)
~/.ai/skills/tmux/bin/tmux_wait.sh "$SESSION_NAME" /tmp/explore-out.txt 600
OUTPUT=$(cat /tmp/explore-out.txt)
```

**新方式：**
```bash
export AG_BIN=~/.ai/skills/ag/ag

$AG_BIN spawn \
  --id "explore-code" \
  --system @explorer.md \
  --input "Explore code" \
  --timeout 10m

$AG_BIN wait "explore-code" --timeout 600
OUTPUT=$($AG_BIN output "explore-code")
$AG_BIN rm "explore-code"
```

详细迁移指南请参考：`~/.ai/skills/MIGRATION-subagent-to-ag.md`

## 输出文件约定

- 目录：`explorer/` 或通过参数指定
- 命名：`<目标名称>.md`
- 格式：标准 Markdown
- 包含：Overview、Tech Stack、Structure、Patterns、Findings、Gotchas

## 示例

### Example 1: 探索本地代码库

```bash
# 用户输入
/skill:explore 探索当前项目的 RPC 处理机制

# 执行
1. 创建 explorer/ 目录
2. 调用 subagent 探索 RPC 模块
3. 输出到 explorer/rpc.md

# 结果示例
explorer/
└── rpc.md  # RPC 处理机制分析
```

### Example 2: 并行探索多个 repo

```bash
# 用户输入
/skill:explore 探索 auth0/go-auth0 和 golang/oauth2 的实现对比

# 执行
1. 创建 explorer/ 目录
2. 并行启动两个 subagent
   - subagent 1: 探索 auth0/go-auth0
   - subagent 2: 探索 golang/oauth2
3. 输出到 explorer/auth0.md 和 explorer/oauth2.md

# 结果示例
explorer/
├── auth0.md   # auth0/go-auth0 分析
└── oauth2.md  # golang/oauth2 分析
```

## 关键规则

- **只读不修改**：探索阶段不修改任何代码
- **专注关键信息**：不深入细节，收集高层理解
- **独立输出文件**：每个目标一个文件，便于后续引用
- **结构化格式**：使用标准格式，便于解析
- **可并行执行**：多个独立目标可以并行探索

## 后续使用

探索结果可以供后续阶段使用：

```markdown
## Brainstorming 输入
基于 explorer/ 目录中的探索结果，制定决策：
- 选择哪种实现方案？
- 借鉴哪些模式？
- 避免哪些问题？

## Speckit 输入
基于 explorer/ 目录中的探索结果，制定计划：
- 现有代码结构是什么？
- 需要修改哪些文件？
- 遵循哪些现有模式？
```

## 与其他技能的协作

```
用户需求
    ↓
┌─────────────────────┐
│  /skill:explore      │  ← 收集信息
└──────────┬──────────┘
           ↓
explorer/*.md
           ↓
┌─────────────────────┐
│  /skill:brainstorming │  ← 决策路线
└──────────┬──────────┘
           ↓
decisions.md
           ↓
┌─────────────────────┐
│  /workflow start feature │  ← 制定计划
└──────────┬──────────┘
           ↓
spec.md, plan.md, tasks.md
```
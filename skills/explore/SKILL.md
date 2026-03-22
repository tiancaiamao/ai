---
name: explore
description: Explore and analyze codebases, repositories, or topics. Outputs findings to independent files for later use by brainstorming or planning phases.
tools: [bash]
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
| **speckit** | 制定计划 | `spec.md`, `plan.md`, `tasks.md` |
| **worker** | 执行实现 | 修改的代码 |
| **review** | 验收代码 | `reviews/*.md` |

## 执行流程

### 1. 确定探索目标

可以是：
- 本地代码库路径
- GitHub repo URL
- 特定文件/模块
- 特定主题/概念

### 2. 创建输出目录

```
explorer/
├── <target1>.md
├── <target2>.md
└── ...
```

### 3. 探索内容

**必收集信息**：
- 项目结构和技术栈
- 核心模块和职责
- 关键模式和方法
- 依赖关系
- 代码约定和风格

**可选收集**（取决于目标）：
- 性能考虑
- 安全考虑
- 扩展性设计
- 已知限制或问题

### 4. 输出格式

```markdown
# Explorer: <目标>

**Date:** YYYY-MM-DD
**Target:** <探索目标>

## Overview
<一句话描述这个代码库/模块/功能>

## Tech Stack
- Language: 
- Framework: 
- Key Libraries: 

## Project Structure
<目录结构>

## Core Components

### Component 1: <名称>
- **File:** `<path>`
- **Responsibility:** <职责>
- **Key APIs:** 
  - `function1()` - <说明>
  - `function2()` - <说明>

### Component 2: <名称>
...

## Key Patterns

### Pattern 1: <模式名称>
**Location:** `<file>:<line>`
```
<code snippet>
```
**Usage:** <使用场景>

## Dependencies
- External: <外部依赖>
- Internal: <内部依赖>

## Conventions
- Coding style: 
- Naming: 
- Error handling: 

## Key Findings
1. <发现 1>
2. <发现 2>
3. <发现 3>

## Gotchas
- <潜在问题或陷阱>

## Relevance to Task
<与当前任务的关联>
```

## Subagent 执行

类似 review 技能，explore 可以通过 subagent 独立执行：

**⚠️ 重要：参考 `/skill:subagent` 技能的最佳实践**

核心要点：
- **必须**使用 persona：`--system-prompt @/path/to/explorer.md`
- **必须**设置 timeout：`--timeout 10m`
- **必须**使用 tmux_wait.sh 等待：`tmux_wait.sh "$SESSION_NAME" 600`
- **长任务描述**写入文件，避免命令行过长
- **必须**设置 bash timeout 参数（对于 >2min 的任务）

**⚠️ Bash Timeout 设置**：
```bash
# 短任务 (<2min): 默认 bash timeout 足够
~/.ai/skills/subagent/bin/start_subagent_tmux.sh -w /tmp/explore-out.txt 5m \
  @/path/to/explorer.md "Explore ..."

# 长任务 (>2min): 必须设置 bash timeout 参数
~/.ai/skills/subagent/bin/start_subagent_tmux.sh -w /tmp/explore-out.txt 10m \
  @/path/to/explorer.md "Explore ..."
#                                              ↑ 在你的 tool call 中设置 "timeout": 660
```

```bash
# 单目标探索
SESSION=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/explore-output.txt \
  10m \
  @/Users/genius/.ai/skills/explore/explorer.md \
  "Explore ~/project/pi-mono's subagent implementation. Write findings to explorer/subagent.md")

SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)
~/.ai/skills/tmux/bin/tmux_wait.sh "$SESSION_NAME" 600

# 查看结果
cat /tmp/explore-output.txt

# 多目标并行探索
SESSION1=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/explore1.txt 10m \
  @/Users/genius/.ai/skills/explore/explorer.md \
  "Explore repo1. Write to explorer/repo1.md")

SESSION2=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/explore2.txt 10m \
  @/Users/genius/.ai/skills/explore/explorer.md \
  "Explore repo2. Write to explorer/repo2.md")

# 等待两个 subagent 完成
~/.ai/skills/tmux/bin/tmux_wait.sh "$(echo $SESSION1 | cut -d: -f1)" 600
~/.ai/skills/tmux/bin/tmux_wait.sh "$(echo $SESSION2 | cut -d: -f1)" 600

# 查看结果
cat /tmp/explore1.txt
cat /tmp/explore2.txt
```

## 输出文件约定

- 目录：`explorer/`
- 命名：`<目标名称>.md`
- 格式：标准 Markdown
- 包含：Overview、Tech Stack、Structure、Patterns、Findings、Gotchas

## 探索 vs 研究

| 维度 | 探索 (explore) | 研究 (research) |
|------|-----------------|-----------------|
| **目标** | 代码库、代码 | 主题、概念、外部资源 |
| **方法** | 读代码、搜索 | 搜索网络、阅读文档 |
| **输出** | 代码结构、模式 | 设计决策、最佳实践 |
| **后续** | brainstorming、speckit | brainstorming |

**使用场景**：
- 探索 → 了解现有代码库
- 研究 → 了解外部实现或设计模式

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

### Example 3: 探索多个相关模块

```bash
# 用户输入
/skill:explore 分析当前项目的任务调度和 worker 池实现

# 执行
1. 创建 explorer/ 目录
2. 探索 scheduler 模块 → explorer/scheduler.md
3. 探索 worker 模块 → explorer/worker.md
4. 探索 queue 模块 → explorer/queue.md

# 结果示例
explorer/
├── scheduler.md
├── worker.md
└── queue.md
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
│  /skill:speckit     │  ← 制定计划
└──────────┬──────────┘
           ↓
spec.md, plan.md, tasks.md
```
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
| **speckit** | 制定计划 | `spec.md`, `plan.md`, `tasks.md` |
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
```

**⚠️ 输出目录**：使用绝对路径写入文件

```bash
# 单目标探索
~/.ai/skills/subagent/bin/start_subagent_tmux.sh -w /tmp/explore-out.txt 10m \
  @/path/to/explorer.md \
  "Explore ~/project/pi-mono.
Write findings to: /tmp/explorer/<target>.md"

# 查看结果
cat /tmp/explorer/*.md
```

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
│  /skill:speckit     │  ← 制定计划
└──────────┬──────────┘
           ↓
spec.md, plan.md, tasks.md
```
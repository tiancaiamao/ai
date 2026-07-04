# PGE Skill — Planner-Generator-Evaluator Orchestration

## What

PGE 模式借鉴 GAN（生成对抗网络）的多 agent 竞争反馈循环，将复杂编码任务拆为三个独立角色——Planner（编排器）、Generator（实现者）、Evaluator（评估者），通过 `ai` CLI 的 `serve`/`send`/`watch`/`kill` 控制子 agent，实现动态的任务拆解-执行-验证闭环。

**触发方式：**
```bash
ai run --role orchestrator "implement dark mode for the web app"
ai serve --role orchestrator --name "orchestrator"
```

`--role orchestrator` 将 system prompt 替换为编排器模板，描述了子 agent 控制协议。

## Why

### 旧方案的问题

`implement` skill 的 `ag task run` 是静态 DAG 调度：
- task 在执行前全部定义好
- scheduler 按固定依赖推进
- 无法根据中间结果调整计划
- review 和实现共享同一个 agent（self-evaluation bias）
- 依赖 `ag` Go binary（外部基础设施）

### PGE 的改进

PGE 的编排器是 **动态的**：
- 根据 spec 拆出第一批 task
- 执行后看结果，决定下一批
- 验证失败时重新规划
- 直到所有验收标准通过
- Generator 和 Evaluator 完全独立（消除 self-evaluation bias）
- 直接用 `ai` CLI（无需外部依赖）

### 理论基础

#### GAN 启发（Anthropic）

Anthropic 的 Prithvi Rajasekaran 发现：
- 把 "生成" 和 "评估" 拆给两个独立 agent，像 GAN 的 Generator 和 Discriminator
- Evaluator 用 Playwright MCP 实际操作页面来评估前端质量，而非只看代码
- 对主观任务（UI 设计）和客观任务（全栈开发）都有效
- **关键发现：Evaluator 的标准本身就是 feedforward control** — 明确告诉 Generator "什么是好的"比事后再审更有效

#### Self-evaluation Bias（Anthropic）

Agent 审查自己的代码时会产生系统性偏差：
- 高估代码质量
- 忽略自己引入的 bug
- 对设计决策的合理性过度自信

**解决方案：** Evaluator 必须是完全独立的 agent，不看到 Generator 的 system prompt 和思考过程。

#### Context Anxiety（Anthropic）

模型接近上下文窗口限制时会：
- 提前结束任务（"看起来差不多了"）
- 跳过边缘情况
- 简化实现

**解决方案：** 不要用 compaction 压缩上下文传给下一个 Generator。而是写结构化的 `state.md`，让每个 Generator 从满上下文窗口开始。

#### Progressive Disclosure（OpenAI）

OpenAI 的 Harness Engineering 实验发现：
- AGENTS.md 应该 ~100 行，只做指针/索引
- Agent 从小入口开始，按需深入
- 不要在 system prompt 里塞入全部信息

**PGE 体现：** spec.md 就是 Generator 的入口点，task description 包含实现所需的所有上下文，Generator 不需要读整个项目。

## Architecture

```
User
  │
  ▼
┌─────────────────────────┐
│  Orchestrator (ai --role│  ← Planner: 你（当前 agent）
│  orchestrator)          │
│                         │
│  1. Write spec.md       │
│  2. Decompose tasks     │
│  3. Spawn generators    │
│  4. Spawn evaluators    │
│  5. Interpret feedback  │
│  6. Adjust plan         │
└────────┬────────────────┘
         │
    ┌────┴────┐
    ▼         ▼
┌────────┐ ┌──────────┐
│Generator│ │Evaluator │  ← Independent agents
│(ai --  │ │(ai --    │
│role    │ │role      │
│coder)  │ │validator)│
└────────┘ └──────────┘
```

**控制流：**
```
Orchestrator ──spawn──► Generator ──output──► Orchestrator ──spawn──► Evaluator
                                                                          │
Orchestrator ◄──result── Evaluator ◄──eval──┘
     │
     ├── all pass ──► next task
     └── some fail ──► create fix task ──► loop (max 3 rounds)
```

## Dependency Skills

| Skill | Purpose |
|-------|---------|
| `subagent` | ai serve/send/watch/kill 的 spawn-monitor-control 模式 |
| `worker-judge` | Worker-Judge 迭代循环的通用框架（PGE 的 Gen-Eval 循环是其特化） |

## Feedforward + Feedback Framework

来自 Martin Fowler 的 Harness Engineering 框架：

| 方向 | 类型 | PGE 中的体现 |
|------|------|-------------|
| 前馈 (Feedforward) | 推断性 | spec.md, task description |
| 前馈 (Feedforward) | 计算性 | 代码生成模板, 文件结构约定 |
| 反馈 (Feedback) | 计算性 | linter, 测试, 构建检查 |
| 反馈 (Feedback) | 推断性 | Evaluator agent 的结构化审查 |

**编排器的核心职责：** 观察 Generator-Evaluator 循环中的重复问题，迭代改进 feedforward（spec 更清晰）和 feedback（Evaluator 标准更严格）。

## Context Management Strategy

### Problem: Context Degradation

长时间运行的 agent 面临两个问题：
1. **Context anxiety** — 接近窗口限制时提前收摊
2. **Noise accumulation** — 中间工具调用、错误修复等无用信息填满窗口

### Solution: Structured State Handoff

```
Generator 1              Generator 2              Generator 3
[full context]    →     [full context]    →     [full context]
reads:                    reads:                   reads:
  spec.md                  spec.md                  spec.md
  task-001.md              state.md                 state.md
                           task-002.md              task-003.md
```

每个 Generator 从干净状态启动，只读必要的文件：
- `spec.md` — 全局目标（稳定，不变）
- `state.md` — 前序工作总结（Generator 1 完成后由 Orchestrator 编写）
- `task-NNN.md` — 当前任务描述

**state.md 结构：**
```markdown
# State

## Completed Tasks
- T001: Add JWT auth — done, files: src/auth/jwt.go, src/api/login.go
- T002: Add RBAC middleware — done, files: src/middleware/rbac.go

## Key Decisions
- Token in http-only cookie (not localStorage)
- Roles: admin, editor, viewer

## Known Issues
- Token refresh not yet implemented (T003)

## What's Next
- T003: Implement token refresh
```

## File Layout

```
.pge/
  spec.md              # Requirements + acceptance criteria
      state.md             # Current state — updated after each task PASS
  tasks/
    001-add-auth.md
    002-add-rbac.md
```

## Key Differences from `implement` Skill

| | implement (ag task) | PGE (ai --role orchestrator) |
|---|---|---|
| Infrastructure | ag Go binary | ai CLI (native) |
| Task definition | Static DAG (tasks.md) | Dynamic (orchestrator decides on-the-fly) |
| Scheduling | Fixed dependency order | Adaptive based on results |
| Validation | Per-task review in same agent | Independent Evaluator agent |
| Failure recovery | Retry ×3, manual after | Orchestrator adjusts plan dynamically |
| Context management | Compaction | Structured state.md handoff |
| Human involvement | Setup only | Spec approval + error escalation |
| Self-evaluation | Review by same agent | Separate Evaluator (no bias) |

## Non-Goals

- Not replacing simple tasks (`ai run "fix this bug"` still works)
- Not building a framework/library — it's a skill (SKILL.md)
- Not multi-user/multi-tenant
- Not modifying `ai` CLI itself (PGE is pure skill layer)
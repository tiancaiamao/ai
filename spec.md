# Explore-Driven Development Workflow

**Date:** 2025-03-22
**Status:** Draft
**Author:** AI Agent

## Overview

创建一个基于探索的技能工作流系统，整合 explore、brainstorming、speckit、worker、review 和 orchestrate 技能，实现从需求到代码的完整流程。

**核心理念**：每个阶段都可以独立运行和验证，流程越多越不容易跑通，所以每一步都要相对可独立调试。

---

## 背景与动机

### 问题诊断

1. **orchestrate 用不起来**：
   - 缺少自动任务分解规则
   - 缺少进度跟踪机制
   - 与 speckit/tasks 没有联动

2. **subagent 不顺畅**：
   - 缺少与 tasks.md 的联动
   - 缺少 Worker persona（执行者心态）

3. **技能之间割裂**：
   - speckit、orchestrate、subagent 各自独立
   - 没有形成流畅的链路

### 参考来源

- **pi-config 工作流**：Scout → Planner → Worker → Reviewer
- **explore 技能**：刚刚创建的探索阶段
- **review 技能**：已验证的 subagent 执行模式

---

## 目标

### 主要目标

1. **Explore 阶段**：收集信息，理解现状
2. **Brainstorming 阶段**：基于收集的信息决策
3. **Speckit 阶段**：制定计划（spec/plan/tasks）
4. **Worker 阶段**：独立执行任务
5. **Review 阶段**：验收代码
6. **Orchestrate 阶段**：串联整个流程

### 设计原则

1. **可独立调试**：每个阶段都可以独立运行和验证
2. **明确的输入/输出**：每个阶段有明确的交付物
3. **文件驱动**：通过文件传递状态和结果
4. **Subagent 友好**：关键阶段支持 subagent 执行

---

## 阶段定义

### 1. Explore 阶段

**职责**：探索代码库、仓库或主题，收集关键信息

**输入**：
- 目标（本地路径、GitHub URL、主题）
- 可选：之前的 brainstorming 结果

**输出**：
- `explorer/*.md`（每个目标一个文件）

**文件格式**：
```markdown
# Explorer: <目标>

## Overview
## Tech Stack
## Project Structure
## Core Components
## Key Patterns
## Key Findings
## Gotchas
```

**独立调试**：`/skill:explore <目标>`

---

### 2. Brainstorming 阶段

**职责**：基于探索结果，决策实现路线

**输入**：
- `explorer/*.md`
- 用户原始需求

**输出**：
- `decisions.md`

**文件格式**：
```markdown
# Decisions

## 路线选择
- [x] 选择：<路线名称>
- [ ] 未选：<路线名称>

## 理由
## 后续计划
```

**独立调试**：`/skill:brainstorming`

---

### 3. Speckit 阶段

**职责**：制定详细的实现计划

**输入**：
- `explorer/*.md`
- `decisions.md`
- 用户需求

**输出**：
- `spec.md`
- `plan.md`
- `tasks.md`

**tasks.md 格式**：
```markdown
- [ ] TASK-1: 描述
  agent: worker
  status: pending
  result: |
    (执行后填写)

- [ ] TASK-2: 描述
  agent: worker
  status: pending
```

**独立调试**：`speckit specify` / `speckit plan` / `speckit tasks`

---

### 4. Worker 阶段

**职责**：执行单个任务

**输入**：
- `tasks.md` 中当前任务

**流程**：
1. 读取 tasks.md
2. 找到 status:pending 的任务
3. 标记 status: in_progress
4. 执行实现
5. 运行测试验证
6. 标记 status: done + 填写 result

**输出**：
- 修改的代码
- `tasks.md` 中任务状态更新

**独立调试**：`/skill:worker <task-id>`

---

### 5. Review 阶段

**职责**：验收代码

**输入**：
- Worker 完成的代码变更
- 对应的 task

**流程**：
1. 读取 tasks.md 中 task 的 result
2. 审查代码变更
3. 运行测试
4. 输出 review.md

**输出**：
- `reviews/<task-id>.md`（P0/P1/P2/P3 问题）

**循环机制**：
```
Worker → Review → 有 P0/P1？ → Worker 修复 → Review 重新验收
                ↓
              无 → Task 完成
```

**独立调试**：`/skill:review <task-id>`

---

### 6. Orchestrate 阶段

**职责**：串联整个流程

**输入**：
- `tasks.md`
- `decisions.md`

**流程**：
1. 读取 tasks.md
2. 对于每个 pending task：
   a. 调用 worker 执行
   b. 调用 reviewer 验收
   c. 如果有 P0/P1 问题：修复后重新 review
   d. 直到 P0/P1 清零
3. 更新 tasks.md 状态
4. 汇总最终结果

**输出**：
- 所有任务完成状态
- 最终汇总

**独立调试**：`/skill:orchestrate`

---

## 文件结构

```
project/
├── explorer/          # 探索结果
│   ├── repo1.md
│   └── repo2.md
├── decisions.md       # 决策文档
├── spec.md            # 规格说明
├── plan.md            # 实现计划
├── tasks.md           # 任务列表
├── reviews/           # 审查结果
│   ├── TASK-1.md
│   └── TASK-2.md
└── artifacts/        # 其他产物
    └── ...
```

---

## 技能列表

| 技能 | 职责 | 输入 | 输出 | 独立调试 |
|------|------|------|------|---------|
| **explore** | 探索收集信息 | 目标 | explorer/*.md | ✅ |
| **brainstorming** | 决策路线 | explorer/*.md | decisions.md | ✅ |
| **speckit** | 制定计划 | decisions.md | spec/plan/tasks | ✅ |
| **worker** | 执行任务 | tasks.md | 代码 + result | ✅ |
| **review** | 验收代码 | 代码变更 | reviews/*.md | ✅ |
| **orchestrate** | 串联流程 | tasks.md | 完成状态 | ✅ |

---

## 验收标准

### Explore 阶段
- [ ] 可以探索本地代码库
- [ ] 可以探索 GitHub repo
- [ ] 可以并行探索多个目标
- [ ] 输出格式符合规范

### Brainstorming 阶段
- [ ] 可以读取 explorer 结果
- [ ] 可以询问用户明确需求
- [ ] 输出决策文档

### Speckit 阶段
- [ ] 可以生成 spec.md
- [ ] 可以生成 plan.md
- [ ] 可以生成 tasks.md（包含 agent:worker）

### Worker 阶段
- [ ] 可以从 tasks.md 读取任务
- [ ] 可以执行任务并更新状态
- [ ] 可以填写 result

### Review 阶段
- [ ] 可以审查代码变更
- [ ] 可以输出 P0/P1/P2/P3 问题
- [ ] 可以循环直到 P0/P1 清零

### Orchestrate 阶段
- [ ] 可以串联 worker + review
- [ ] 可以跟踪 tasks.md 进度
- [ ] 可以汇总最终结果

---

## 实施优先级

### Phase 1: 完善 Explore（已完成）
- [x] 创建 explore 技能
- [x] 创建 explorer persona

### Phase 2: 完善 Worker
- [ ] 创建 worker 技能
- [ ] 创建 worker persona
- [ ] 与 tasks.md 联动

### Phase 3: 改进 Orchestrate
- [ ] 串联 explore + brainstorming + speckit
- [ ] 串联 worker + review
- [ ] 添加进度跟踪

### Phase 4: 端到端测试
- [ ] 测试完整工作流
- [ ] 验证每个阶段独立可调试

---

## 风险与开放问题

1. **Worker 的测试验证**：如何确保 Worker 正确运行测试？
2. **Review 的循环机制**：如何自动触发 Worker 修复？
3. **状态管理**：tasks.md 的状态更新是否可靠？

---

## 参考资料

- pi-config Scout agent: `/Users/genius/project/pi-config/agents/scout.md`
- pi-config Worker agent: `/Users/genius/project/pi-config/agents/worker.md`
- pi-config Planner agent: `/Users/genius/project/pi-config/agents/planner.md`
- pi-config Reviewer agent: `/Users/genius/project/pi-config/agents/reviewer.md`
- explore 技能: `/Users/genius/project/ai/.worktrees/explore-skill/skills/explore/SKILL.md`
- review 技能: `/Users/genius/project/ai/skills/review/SKILL.md`
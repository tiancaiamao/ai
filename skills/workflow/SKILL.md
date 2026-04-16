---
name: workflow
description: 对话式开发流程编排。通过自然语言驱动 brainstorm → spec → plan → implement，无需用户手工执行命令。
---

# Workflow — Conversation Interface

Workflow 是一个**对话接口**，不是命令行教程。

用户只需要表达意图，agent 负责在后台协调 skills、状态文件和执行脚本。

## User Contract (对用户)

用户应通过自然语言触发流程，例如：

- "开始一个 feature workflow：实现用户注册"
- "继续下一个阶段"
- "先给我看当前进度和阻塞点"
- "进入 implement 阶段并用 team mode 执行"
- "暂停 / 恢复这个 workflow"

**不要要求用户手动执行 shell 命令。**

## Agent Contract (对 agent)

agent 必须：

1. 负责 workflow 状态管理（开始、推进、暂停、恢复、完成）。
2. 根据当前阶段自动调用对应 skill：
   1. `brainstorm`
   2. `spec`
   3. `plan`
   4. `implement`
3. 在关键节点向用户汇报：
   1. 当前阶段
   2. 已完成产物
   3. 下一步动作
   4. 风险/阻塞
4. 仅在必要时向用户提确认问题（例如范围变更、风险接受）。

## Templates

| Template | Flow | Use Case |
|----------|------|----------|
| `feature` | brainstorm → spec → plan → implement | 新功能开发（重点） |
| `bugfix` | explore → plan → implement | 缺陷修复 |
| `spike` | brainstorm → document | 调研验证 |
| `refactor` | explore → plan → implement → verify | 重构 |
| `hotfix` | implement | 紧急修复 |
| `security` | explore → plan → implement → verify | 安全审计 |

## Phase Behavior

### Brainstorm
- 目标：收敛需求与边界，形成可被批准的设计。
- 输出：`design.md`。
- 交互：向用户确认设计方向后再进入下一阶段。

### Spec
- 目标：把设计变成结构化规格（用户故事、验收标准）。
- 输出：`SPEC.md`。
- 交互：获取用户对规格的明确确认。

### Plan
- 目标：把 SPEC 拆成可执行任务与依赖。
- 输出：`PLAN.yml` + `PLAN.md`。
- 交互：展示任务分组、依赖和风险，等待用户确认。

### Implement
- 默认使用 team mode（中大型任务）。
- 输出：代码变更 + 测试结果 + `impl-report.md`。
- 交互：定期汇报进度、失败重试结果、剩余任务。

## Implement Team Mode (internal)

implement 阶段内部可使用 `ag` 的 team/task 能力（`team init/use`、`task import-plan`、`task next`、依赖调度、收尾清理），但这些属于**实现细节**，不应要求用户手动操作。

## Artifacts

```text
.workflow/
├── STATE.json
└── artifacts/
    └── <template>/
        └── <workflow-name>/
            ├── design.md
            ├── SPEC.md
            ├── PLAN.yml
            ├── PLAN.md
            └── impl-report.md
```

## Conversation-First Rules

1. 任何“开始/继续/暂停/恢复/状态查询”都应可通过自然语言完成。
2. 不把 `workflow-ctl`、`ag`、shell 参数作为用户主交互方式。
3. 当后台命令失败，向用户解释失败原因和下一步建议，而不是让用户自己跑命令。
4. 只在用户明确要求底层命令时，才展示命令细节。

---
name: plan
description: "[DEPRECATED] 已被 PGE 模式替代。请使用 pge 技能。"
deprecated: true
redirect: pge
---

# Plan → 已迁移至 PGE

此技能已被 **PGE（Planner-Generator-Evaluator）** 模式替代。

## 迁移说明

**旧流程：** brainstorm → plan（ag worker-judge loop → tasks.md）→ implement（ag task run）
**新流程：** PGE（动态编排，ai serve/send/watch/kill 控制 Generator + Evaluator）

| 旧 (plan) | 新 (PGE) |
|-----------|----------|
| planner.md worker-judge loop | Orchestrator 直接拆解任务 |
| reviewer.md 验证 | 独立 Evaluator agent |
| 导入 ag task 队列 | `.pge/tasks/` 目录 |
| ag agent spawn/wait | ai serve + tmux |

## 保留资源

以下文件作为参考保留，编写 PGE Generator/Evaluator 的 system prompt 时可参考：

- `prompts/planner.md` — Task breakdown 的 self-containedness 规则
- `prompts/reviewer.md` — Plan review 的 Must Pass 标准
- `cmd/plan-lint/` — tasks.md 格式验证工具

## 使用新流程

```bash
# 不再需要单独的 plan 步骤
# PGE 的 Orchestrator 直接完成需求分析 + 任务拆解 + 验证闭环
ai run --role orchestrator "implement dark mode for the web app"
```

详见 `~/.ai/skills/pge/SKILL.md`
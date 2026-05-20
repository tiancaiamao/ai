---
name: implement
description: "[DEPRECATED] 已被 PGE 模式替代。请使用 pge 技能。"
deprecated: true
redirect: pge
---

# Implement → 已迁移至 PGE

此技能已被 **PGE（Planner-Generator-Evaluator）** 模式替代。

## 迁移说明

**旧流程：** plan → ag task import-plan → ag task run（scheduler + worker + reviewer）
**新流程：** PGE Orchestrator → Generator（ai serve）→ Evaluator（ai serve）

| 旧 (implement) | 新 (PGE) |
|----------------|----------|
| ag task scheduler | Orchestrator 动态调度 |
| ag agent spawn worker | ai serve --role coder（tmux 后台）|
| ag agent spawn reviewer | ai serve --role validator（独立 Evaluator）|
| Static DAG | Dynamic（根据结果调整）|
| ag task run --detach | Orchestrator 逐 task 控制 |

## 保留资源

以下文件作为参考保留，编写 PGE Generator/Evaluator 的 system prompt 时可参考：

- `prompts/implementer.md` — Generator prompt 参考（YAGNI、self-review checklist）
- `prompts/spec-reviewer.md` — Evaluator 的 spec compliance 检查维度
- `prompts/quality-reviewer.md` — Evaluator 的 code quality 检查维度

## 使用新流程

```bash
# 不再需要 implement 技能
# PGE 的 Orchestrator + Generator + Evaluator 完成完整的实现-验证闭环
ai run --role orchestrator "implement dark mode for the web app"
```

详见 `~/.ai/skills/pge/SKILL.md`
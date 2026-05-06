---
name: brainstorm
description: 通过对话探索用户意图，产出 design.md。design 覆盖 5 个内容维度，让无上下文的 subagent 也能理解全貌。
---

# Brainstorm

通过对话探索用户意图，产出 `design.md`。

## Hard Gate

```
NO IMPLEMENTATION until design.md is approved by the user.
```

如果发现自己写代码，立刻停下。

## The Process

### Step 1: Understand Context

先了解环境：
- 读 AGENTS.md、README、目录结构
- 快速扫描相关文件（不深入）
- 确定领域类型（CLI、web、library、API）

### Step 2: Dependency Inversion Interview

一次问一个问题。从期望的结果倒推：

**好问题（结果导向）：**
- "做完之后用户能做什么？"
- "成功是什么样的？"
- "如果明天就上线，你先测什么？"

**坏问题（实现导向）：**
- "用 PostgreSQL 还是 MySQL？"
- "要不要加个缓存层？"

在理解问题之前不要提技术方案。优先用选择题。

### Step 3: Explore Codebase (Conditional)

只在以下情况 explore：
- 需要集成现有系统
- 修改现有行为
- 技术约束不明确

跳过 explore 的情况：
- 全新功能
- 需求明确
- 用户已提供技术上下文

### Step 4: Write design.md

把讨论结果写成 `design.md`，覆盖以下 **5 个内容维度**：

**1. 现状（改之前长什么样）**
- 具体到文件路径、数据结构、函数签名、关键代码路径
- 不是泛泛的 "系统有这个问题"
- 检验标准：subagent 读完这部分不需要去读代码就知道 "改之前是什么"

**2. 为什么要改（具体痛点，不是 "为了更好"）**
- 性能瓶颈在哪个函数、哪次调用
- 哪个设计决策导致了当前限制
- 量化数据更好（性能、延迟、错误率）

**3. 关键设计决策和取舍**
- 2-3 个候选方案 + 每个的优劣 + 选择理由
- 不是 "我选了 A 因为它好"，而是 "A 牺牲了 X 换来了 Y，在当前场景下 Y 更重要"

**4. 怎么做（subagent 不读代码就能动手的程度）**
- 关键接口定义（Go 代码、伪代码、类型签名）
- 数据流 / 调用链
- 涉及哪些文件需要改/新建
- 状态机、流程图用文字或 mermaid 描述

**5. 边界条件和特殊情况**
- corner cases：错误处理、并发、大数据量、兼容性
- 不是只写 happy path

**可选但建议有**：Goals / Non-Goals、Impacts & Risks
**不需要**：测试计划、任务拆解、八股模板

### Step 5: Present & Gate

1. 向用户展示 design.md 摘要（分段展示，不要一次贴一整篇）
2. 运行 `wf approve --message "<用户原话>"`
3. 运行 `wf advance --output design.md`
4. 提示用户下一步：plan skill

如果用户有修改意见，回到 Step 2 迭代。

## ⛔ MANDATORY — Self-Check

| 断言 | 触发条件 | 修正 |
|------|----------|------|
| 未读技能文件 | 开始 brainstorm 但没读这个 SKILL.md | 先读完 |
| 未 approve 就 advance | `wf advance` 返回错误 | 先 `wf approve --message "用户原话"` |
| self-approve | `--message` 不是用户说的话 | 等用户确认 |
| 未展示产出就问确认 | design.md 存在但未向用户展示 | 先展示摘要 |
| 缺少内容维度 | design.md 缺少 5 个维度中的任何一个 | 补充缺失维度 |

## Skill Composition

```
brainstorm (this skill) → design.md
    ↓ (gate approved)
    ↓
plan → implement
```

也可以直接跳入：
- 已有 explore 结果 → "根据 explore 写 design"
- 直接 "写 design：实现 xxx" → 跳过 interview
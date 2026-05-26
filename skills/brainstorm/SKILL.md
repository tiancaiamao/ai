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

**5. 验收场景（每个 P0 feature 必须有）**

对于 design 中标记为 P0 的每个功能，必须定义 **行为级验收场景**。这不是测试用例，而是 "做完之后，观察者能看到什么行为"。

格式：
```markdown
### P0 Feature: <feature name>
**Acceptance Scenarios:**
1. <concrete observable behavior>
2. <concrete observable behavior>
3. <edge case behavior>
```

示例：
```markdown
### P0 Feature: Agent Loop
**Acceptance Scenarios:**
1. User sends prompt → agent returns text response
2. LLM returns tool_call → tool executes → result fed back → agent continues
3. LLM returns 3 concurrent tool_calls → all execute in parallel
4. MaxTurns=2 → agent stops after 2 turns even if LLM wants more
5. Context cancelled mid-stream → agent stops and emits AgentEnd
```

**为什么这很重要：** 验收场景是实现和验证的基准。如果 design 没有定义具体场景，实现时就只能靠 "go test passes" 这种不可靠的验证标准。

**6. 边界条件和特殊情况**
- corner cases：错误处理、并发、大数据量、兼容性
- 不是只写 happy path

**7. Completeness Checklist 对照（如果 explore 阶段有输出）**

如果 explorer 输出中包含 Completeness Checklist，design 必须逐项回应。对于每一项，明确标注：

- ✅ Covered — 本 design 包含此功能（指向对应的 feature/section）
- ⏸️ Deferred — 推迟到后续版本（必须说明理由和影响）
- 🔀 Merged — 合并到其他功能中（必须说明合并到哪里）

如果 explore 的 checklist 有 19 个包但 design 只规划了 8 个包，剩余 11 个必须逐个标注。不允许静默省略。

**可选但建议有**：Goals / Non-Goals、Impacts & Risks
**不需要**：测试计划、任务拆解、八股模板

### Step 5: Present & Gate

1. 向用户展示 design.md 摘要（分段展示，不要一次贴一整篇）
2. **等用户明确确认**（例如 "可以"、"没问题"、"LGTM"）才算 gate 通过
3. 确认后，用户可自行选择下一步实现方式。

Gate 规则：
- 用户没说 OK 就不算通过，不能进入下一步
- 不能自己替用户确认
- 用户有修改意见 → 回到 Step 2 迭代

## ⛔ MANDATORY — Self-Check

| 断言 | 修正 |
|------|------|
| 未读技能文件 | 先读完本 SKILL.md 再开始 |
| self-approve | 等用户明确说 OK，不能自己确认 |
| 未展示产出就问确认 | 先展示 design.md 摘要 |
| 缺少内容维度 | design.md 必须覆盖 7 个维度（含验收场景和 checklist 对照），缺了就补 |
| P0 feature 没有验收场景 | 每个 P0 feature 必须有具体的 Acceptance Scenarios |
| explore checklist 未逐项回应 | 如果有 explore 输出，每个 checklist 项必须标注 covered/deferred/merged |
| scope 定义自相矛盾 | Success criteria 和 feature list 不能矛盾。不能同时说 "drop-in replacement" 又省略功能 |

## Skill Composition

```
brainstorm (this skill) → design.md
    ↓ (user approved)
    ↓
用户选择实现方式：直接实现、pge 编排、或其他
```

也可以直接跳入：
- 已有 explore 结果 → "根据 explore 写 design"
- 直接 "写 design：实现 xxx" → 跳过 interview
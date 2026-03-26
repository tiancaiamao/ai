---
name: brainstorming
description: Use BEFORE speckit for complex or unclear features. Explores requirements, proposes approaches, and creates design doc. Skip for simple, well-defined tasks.
---

# Brainstorming Ideas Into Designs

## When to Use This Skill

```
User request → Is the feature complex or unclear? → Involves architecture changes?
                    ↓                           ↓
              YES → Use brainstorming      YES → MUST clarify requirements
                    ↓                           ↓
              Then use speckit             (this skill: clarification mode)
```

**Use brainstorming when:**
- Feature is complex or has many unknowns
- Multiple approaches are possible
- Need to explore trade-offs
- Requirements are unclear
- **Involves architectural changes** (unify, consolidate, refactor, integrate)

**Skip brainstorming when:**
- Task is simple and well-defined (e.g., "add a field to struct")
- Requirements are crystal clear
- Only one obvious approach exists

## Two Modes

### Mode 1: Design Brainstorming (default)
Turn ideas into fully formed designs through collaborative dialogue.

### Mode 2: Requirements Clarification
**Trigger**: After explore phase, before plan phase, especially for architecture changes.

**Output Format**:
```markdown
## 我理解的任务目标
- **功能目标**: [做什么] - What it does
- **设计目标**: [为什么] - Why we're doing it
- **架构约束**: [不能做什么] - What constraints exist

## 需要确认的问题

### 1. [关键决策点 A]
- 选项 1: ... (pros/cons)
- 选项 2: ... (pros/cons)
- **我的建议**: ... (because ...)

### 2. [关键决策点 B]
- 选项 1: ...
- 选项 2: ...
- **我的建议**: ...

请确认我的理解是否正确，或者纠正。
```

**Example**:
```markdown
## 我理解的任务目标
- **功能目标**: 实现命令注册系统
- **设计目标**: 统一 rpc/server 和 aiclaw 两套命令实现，减少代码重复
- **架构约束**: agent core 应该保持纯净，命令处理应该在 agent 外部

## 需要确认的问题

### 1. 命令注册应该放在哪个包？
- 选项 A: pkg/agent (在 agent 内部)
  - Pros: 简单，agent 可以直接使用
  - Cons: agent 变得臃肿，违反单一职责
- 选项 B: pkg/command (独立包)
  - Pros: 可复用，agent 保持纯净
  - Cons: 需要额外的依赖管理
- **我的建议**: 选项 B - 独立包，因为目标就是统一实现，让 rpc/server 和 aiclaw 都能使用

### 2. rpc/server 和 aiclaw 的关系？
- 选项 A: 都依赖新的 pkg/command
- 选项 B: 只在 ai 项目内部使用
- **我的建议**: 选项 A - 统一依赖同一个实现

请确认我的理解是否正确。
```

## Overview

Help turn ideas into fully formed designs through collaborative dialogue.

Start by understanding the current project context, then ask questions one at a time to refine the idea. Once you understand what you're building, present the design and get user approval.

<HARD-GATE>
Do NOT invoke speckit, write any code, scaffold any project, or take any implementation action until you have presented a design and the user has approved it.
</HARD-GATE>

## Anti-Pattern: "This Is Too Simple To Need A Design"

Every project goes through this process. A todo list, a single-function utility, a config change — all of them. "Simple" projects are where unexamined assumptions cause the most wasted work. The design can be short (a few sentences for truly simple projects), but you MUST present it and get approval.

## Process Flow

```
1. Explore project context → Check files, docs, recent commits
2. Ask clarifying questions → One at a time, understand purpose/constraints/success
3. Propose 2-3 approaches → With trade-offs and your recommendation
4. Present design → Scale to complexity, get approval after each section
5. Write design doc → Save to docs/plans/YYYY-MM-DD-<topic>-design.md
6. Hand off to speckit → User runs speckit to implement
```

## The Process

**Understanding the idea:**
- Check out the current project state first (files, docs, recent commits)
- Ask questions one at a time to refine the idea
- Prefer multiple choice questions when possible, but open-ended is fine too
- Only one question per message
- Focus on understanding: purpose, constraints, success criteria

**Exploring approaches:**
- Propose 2-3 different approaches with trade-offs
- Present options conversationally with your recommendation and reasoning
- Lead with your recommended option and explain why

**Presenting the design:**
- Once you believe you understand what you're building, present the design
- Scale each section to its complexity: a few sentences if straightforward, up to 200-300 words if nuanced
- Ask after each section whether it looks right so far
- Cover: architecture, components, data flow, error handling, testing
- Be ready to go back and clarify if something doesn't make sense

## After the Design

**Documentation:**
- Write the validated design to `docs/plans/YYYY-MM-DD-<topic>-design.md`
- Commit the design document to git

**Handoff:**
- Tell user: "Design complete. Run `speckit` to start implementation."
- Do NOT invoke speckit automatically - let user decide when to proceed

## Key Principles

- **One question at a time** - Don't overwhelm with multiple questions
- **Multiple choice preferred** - Easier to answer than open-ended when possible
- **YAGNI ruthlessly** - Remove unnecessary features from all designs
- **Explore alternatives** - Always propose 2-3 approaches before settling
- **Incremental validation** - Present design, get approval before moving on
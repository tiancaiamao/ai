---
name: brainstorming
description: Use BEFORE speckit for complex or unclear features. Explores requirements, proposes approaches, and creates design doc. Skip for simple, well-defined tasks.
---

# Brainstorming Ideas Into Designs

## When to Use This Skill

```
User request → Is the feature complex or unclear?
                    ↓
              YES → Use brainstorming FIRST
                    ↓
              Then use speckit
                    
              NO → Use speckit directly
```

**Use brainstorming when:**
- Feature is complex or has many unknowns
- Multiple approaches are possible
- Need to explore trade-offs
- Requirements are unclear

**Skip brainstorming when:**
- Task is simple and well-defined (e.g., "add a field to struct")
- Requirements are crystal clear
- Only one obvious approach exists

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
---
name: brainstorm
description: Use BEFORE any creative work. Explores user intent through dependency inversion, produces a validated design. Every project goes through this regardless of perceived simplicity.
---

# Brainstorm

Turn ideas into validated designs through collaborative dialogue.

## Hard Gate

```
NO IMPLEMENTATION until design is approved by the user.
This applies to EVERY project regardless of perceived simplicity.
```

If you catch yourself writing code before design approval, stop immediately.

## The Process

### Step 1: Understand Context

Read the room before asking questions:
- What project is this? (scan AGENTS.md, README, directory structure)
- What already exists? (quick file scan, don't deep-dive)
- What's the domain? (web app, CLI tool, library, API?)

### Step 2: Dependency Inversion Interview

Ask questions **one at a time**. Use dependency inversion: start from the
desired outcome and work backwards to what's needed.

**Good questions (outcome-first):**
- "What should the user be able to do when this is done?"
- "What does success look like?"
- "If I shipped this tomorrow, what would you test first?"

**Bad questions (implementation-first):**
- "Should we use PostgreSQL?"
- "Do you want a REST API?"
- "Should I add a cache layer?"

Don't propose technical solutions. Understand the problem first.

**Prefer multiple choice** when possible — easier for the user to answer.

### Step 3: Explore Codebase (Conditional)

Only explore if the requirement touches existing features or has technical
ambiguity. Use the `explore` skill for this.

**Do explore when:**
- Integrating with existing systems
- Modifying existing behavior
- Technical constraints unclear

**Skip when:**
- Greenfield feature
- Requirements are unambiguous
- User already provided technical context

### Step 4: Present Design

Present the design in chunks, not a wall of text. Each chunk should be
short enough to read and digest.

**Design structure:**

```
## Problem
[What we're solving, in user language]

## Approach
[1-3 sentences about the approach]

## User Stories (ordered by priority)
1. (P1) As a [role], I can [action] so that [benefit]
2. (P2) As a [role], I can [action] so that [benefit]

## Scope
- In scope: [what we're building]
- Out of scope: [what we're NOT building]

## Open Questions
- [anything still unclear]
```

**YAGNI ruthlessly** — cut anything that isn't needed for the first version.
Always propose 2-3 approaches before settling on one.

### Step 5: Get Approval

Wait for explicit user approval. "Looks good", "go ahead", "approved" all
count. "Cancel" terminates.

If user has concerns, go back to Step 2 and refine.

## After Approval

1. Write the validated design to disk
2. Offer next step: "Design approved. Next: write a spec? Or jump to planning?"

The user chooses. Don't assume the path.

## Anti-Patterns

- ❌ "This is too simple to need a design" — every project goes through this
- ❌ Asking 5 questions at once — one at a time
- ❌ Proposing technical solutions before understanding the problem
- ❌ Skipping approval because "it's obvious"
- ❌ Writing a 20-page design doc — keep it short, iterate

## Skill Composition

```
brainstorm (this skill)
    ↓ (design approved)
    ↓
spec → plan → implement
    or
plan → implement (if user wants to skip spec)
```
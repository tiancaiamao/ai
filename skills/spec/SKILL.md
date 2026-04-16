---
name: spec
description: Write a structured feature specification from a brainstorm design or user requirements. Produces SPEC.md with prioritized user stories, success criteria, and clear scope boundaries.
---

# Spec

Write a structured specification that is precise enough for an LLM to
implement without guessing.

## When to Use

- After brainstorm produces an approved design
- When user says "write a spec" or "specify this"
- As Phase 1 of a feature workflow

## Input

- A brainstorm design (from the `brainstorm` skill)
- Or direct user requirements (if skipping brainstorm)
- Codebase exploration results (from the `explore` skill), if available

## The Spec Format

Write `SPEC.md` following this structure:

```markdown
# Feature: [Name]

## Summary
[1-2 sentences. What this does and why.]

## User Stories

### US1: [Title] (P1) 🎯 MVP

[Describe the user journey in plain language.]

**Independent test:** [How to verify this story works on its own.]

### US2: [Title] (P2)

[Description.]

**Independent test:** [How to verify independently.]

## Functional Requirements

- FR-001: System MUST [specific behavior]
- FR-002: System MUST [specific behavior]

## Non-Functional Requirements

- NFR-001: [performance, security, etc. if applicable]

## Out of Scope

- [Explicitly NOT doing in this iteration]

## Success Criteria

- SC-001: [Measurable outcome, e.g., "User can complete X in under Y steps"]
- SC-002: [Measurable outcome]

## Technical Context

[Only if known from exploration. If unknown, leave blank — plan phase will investigate.]

- Current system: [what exists]
- Integration points: [what it touches]
- Constraints: [technical limits]
```

## Key Rules

### User Stories Must Be Independently Testable

Each story should be a viable slice of functionality:
- Can be developed alone
- Can be tested alone
- Can be demonstrated alone

P1 = MVP. If you only ship P1, users still get value.

### Success Criteria Must Be Measurable

❌ "The system should be fast"
✅ "API responds in under 200ms at p99"

❌ "Good user experience"
✅ "User completes registration in under 3 steps"

### Cut Ruthlessly

- If a requirement doesn't map to a user story, question whether it's needed
- If a user story isn't P1, ask: can we ship without it?
- "We might need this later" → Out of Scope

### Separate What from How

Spec describes **what** the system does, not **how** it's implemented.
Technical decisions go in the plan, not the spec.

## Process

1. **Read inputs** — brainstorm design, exploration results, user requirements
2. **Draft spec** — follow the format above
3. **Present to user** — in digestible chunks
4. **Iterate** — address feedback
5. **Get approval** — explicit sign-off
6. **Save** — write SPEC.md to the project

## Output

- `SPEC.md` — the approved specification

## Skill Composition

```
brainstorm → spec (this skill) → plan → implement
                                    or
                        direct → spec → plan → implement
```
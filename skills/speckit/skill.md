---
name: speckit
description: The MAIN workflow for feature development. Creates spec.md → plan.md → tasks.md, then implements. Use this for ANY feature work unless brainstorming is needed first.
---

# Spec-Driven Development

This is the **primary workflow** for developing features. It guides you from requirements to working code through a structured process.

## ⚠️ CRITICAL: Interactive Mode Required

**Default behavior is INTERACTIVE, not automatic.** You MUST pause at each phase gate and get explicit user approval before proceeding.

```
┌─────────────┐  ⏸️   ┌─────────────┐  ⏸️   ┌─────────────┐  ⏸️   ┌─────────────┐
│  SPECIFY    │ ──►  │    PLAN     │ ──►  │   TASKS     │ ──►  │ IMPLEMENT   │
│  spec.md    │      │  plan.md    │      │  tasks.md   │      │  code       │
└─────────────┘      └─────────────┘      └─────────────┘      └─────────────┘
       ↓                    ↓                    ↓
   Review &            Review &             Review &
   Approve             Approve              Approve
```

## Phase Gates (MANDATORY)

| Gate | When | Required Action |
|------|------|-----------------|
| 🔷 SPEC COMPLETE | After creating spec.md | Present summary, ask questions, get approval to proceed to PLAN |
| 🔷 PLAN COMPLETE | After creating plan.md | Present key decisions, discuss alternatives, get approval to proceed to TASKS |
| 🔷 TASKS COMPLETE | After creating tasks.md | Show task list, confirm scope, get approval to start IMPLEMENTATION |

**Do NOT auto-proceed without explicit user confirmation like:**
- "Looks good, proceed"
- "Yes, continue to plan"
- "That works"

**If user seems hesitant or asks questions, stay in current phase and iterate.**

## When to Use

```
Feature request → Is it complex/unclear?
                      ↓
                YES → Use brainstorming first, then speckit
                NO  → Use speckit directly
```

**Use speckit for:**
- Any new feature development
- Bug fixes that need planning
- Refactoring with clear goals

**Use brainstorming first if:**
- Requirements are unclear
- Multiple approaches exist
- Feature is complex

## Workflow Overview

### Phase 1: SPECIFY → spec.md

**Goal**: Create a clear, testable specification.

**Process:**
1. Gather requirements from $ARGUMENTS
2. Ask clarifying questions - be curious, dig deeper
3. Create spec.md
4. 🔷 PHASE GATE: Present spec summary, get approval

**See [references/specify.md](references/specify.md) for detailed guidance.**

### Phase 2: PLAN → plan.md

**Prerequisite**: User approved spec.md

**Goal**: Create a technical implementation plan.

**Process:**
1. Read spec.md for requirements
2. Explore codebase to understand existing patterns
3. Create plan.md with technical context, data model, API contracts, etc.
4. 🔷 PHASE GATE: Present plan, discuss trade-offs, get approval

**See [references/plan.md](references/plan.md) for detailed guidance.**

### Phase 3: TASKS → tasks.md

**Prerequisite**: User approved plan.md

**Goal**: Create an actionable task checklist.

**Process:**
1. Read plan.md for implementation details
2. Break down into small, testable tasks (15-30 min each)
3. Order by dependency (foundation first)
4. Create tasks.md
5. 🔷 PHASE GATE: Show task list, get approval

**See [references/tasks.md](references/tasks.md) for detailed guidance.**

### Phase 4: IMPLEMENT → code

**Prerequisite**: User approved tasks.md

**Goal**: Execute tasks systematically.

**Process:**
1. Validate checklist
2. Verify project setup
3. For each task: identify → implement → verify → mark complete
4. Use test-driven-development skill

**See [references/implement.md](references/implement.md) for detailed guidance.**

## Usage

1. **Full workflow**: Just say `speckit` with your feature idea
2. **Continue**: Say `speckit` to resume from current phase (still respects phase gates)
3. **Specific phase**: `speckit specify`, `speckit plan`, `speckit tasks`, `speckit implement`

## Auto-Detection Logic

When invoked without a specific phase, check for existing files:

| Files Present | Resume At | Note |
|---------------|-----------|------|
| None | SPECIFY | Start fresh |
| spec.md only | PLAN GATE | Ask if spec is approved first |
| spec.md + plan.md | TASKS GATE | Ask if plan is approved first |
| spec.md + plan.md + tasks.md | IMPLEMENT GATE | Ask if ready to start coding |

## Output Files

All artifacts go in current working directory:

```
./
├── spec.md      # Feature specification
├── plan.md      # Technical implementation plan
└── tasks.md     # Actionable task checklist
```

## Completion Checklist

Before marking all tasks complete:

- [ ] All tests pass
- [ ] All acceptance criteria from spec.md met
- [ ] No regressions in existing functionality
- [ ] Documentation updated if needed

## Examples

See [examples/](examples/) for complete workflow demonstrations.

---

**User's Request:**

$ARGUMENTS
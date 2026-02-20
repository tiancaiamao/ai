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

## Workflow

### Phase 1: SPECIFY → spec.md

**Goal**: Create a clear, testable specification.

1. **Gather requirements** from $ARGUMENTS
2. **Ask clarifying questions** - be curious, dig deeper
3. **Create spec.md** with:
   - Feature overview
   - User stories (P1/P2/P3 priority)
   - Acceptance criteria
   - Success criteria (measurable)
4. **🔷 PHASE GATE**: Present spec summary, ask for feedback, get approval

```
Template for gate message:
---
📋 **spec.md created!** Here's the summary:

[2-3 bullet summary of what we're building]

**Key decisions:**
- [Decision 1]
- [Decision 2]

**Questions for you:**
- [Any open questions]

Ready to proceed to planning? Any changes needed?
---
```

### Phase 2: PLAN → plan.md

**Goal**: Create a technical implementation plan.

**Prerequisite**: User approved spec.md

1. **Read spec.md** for requirements
2. **Explore codebase** to understand existing patterns
3. **Create plan.md** with:
   - Technical context (stack, architecture)
   - Data model changes
   - API contracts
   - Security considerations
   - Implementation order
4. **🔷 PHASE GATE**: Present plan, discuss trade-offs, get approval

```
Template for gate message:
---
📐 **plan.md created!** Key technical decisions:

**Architecture:**
- [Key architectural choice]

**Approach:**
- [Implementation approach]

**Trade-offs considered:**
- [Trade-off 1]: chose X because...
- [Trade-off 2]: chose Y because...

**Scope:**
- [What's included]
- [What's NOT included]

Does this approach work for you? Any concerns before we break this into tasks?
---
```

### Phase 3: TASKS → tasks.md

**Goal**: Create an actionable task checklist.

**Prerequisite**: User approved plan.md

1. **Read plan.md** for implementation details
2. **Break down** into small, testable tasks
3. **Create tasks.md** with:
   - Phases: Setup → Foundation → User Stories → Polish
   - Each task: `- [ ] T001 Description`
   - Mark parallelizable tasks with `[P]`
4. **🔷 PHASE GATE**: Show task list, confirm scope, get approval

```
Template for gate message:
---
📝 **tasks.md created!** 

**Task breakdown:**
- Setup: X tasks
- Foundation: X tasks  
- User Stories: X tasks
- Polish: X tasks

**Total: X tasks** (estimated Y parallel tracks)

**Critical path:** [Key dependencies]

Ready to start implementation? Any tasks to add/remove?
---
```

### Phase 4: IMPLEMENT → code

**Goal**: Execute tasks systematically.

**Prerequisite**: User approved tasks.md

1. Find first unchecked task
2. **Use test-driven-development skill** to implement
3. Run tests
4. Mark complete: `[ ]` → `[X]`
5. Repeat

**Stop and ask** if:
- Blocked by unclear requirement
- Found unexpected issue
- Major decision needed

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

**Starting fresh (interactive):**
```
User: speckit I want to add user authentication

AI: I'll help you spec out user authentication. A few questions:
    - What auth methods? (email/password, OAuth, magic links?)
    - Need password reset?
    - Session management preferences?
    [... gathers requirements, creates spec.md]

AI: 📋 **spec.md created!** Here's what we're building:
    - Email/password auth with bcrypt hashing
    - JWT session tokens (24h expiry)
    - Password reset via email
    
    Ready to proceed to planning?

User: Looks good, let's plan it

AI: [Creates plan.md, presents for review]
    
AI: 📐 **plan.md created!** Using:
    - bcrypt for passwords
    - JWT with refresh tokens
    
    Does this work?

User: Yes, create tasks

AI: [Creates tasks.md, presents for review]
...
```

**Continuing from existing spec:**
```
User: speckit

AI: I see spec.md exists. Before I proceed to planning, let me confirm:
    The spec covers email/password auth with JWT sessions.
    Is this spec approved? Ready to move to planning?

User: Yes, proceed

AI: [Continues to PLAN phase]
```

---

**User's Request:**

$ARGUMENTS
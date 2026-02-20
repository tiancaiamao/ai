---
name: speckit
description: The MAIN workflow for feature development. Creates spec.md → plan.md → tasks.md, then implements. Use this for ANY feature work unless brainstorming is needed first.
---

# Spec-Driven Development

This is the **primary workflow** for developing features. It guides you from requirements to working code through a structured process.

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

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│  SPECIFY    │ ──► │    PLAN     │ ──► │   TASKS     │ ──► │ IMPLEMENT   │
│  spec.md    │     │  plan.md    │     │  tasks.md   │     │  code       │
└─────────────┘     └─────────────┘     └─────────────┘     └─────────────┘
```

## Usage

1. **Full workflow**: Just say `speckit` with your feature idea
2. **Continue**: Say `speckit` to auto-detect current phase and continue
3. **Specific phase**: `speckit specify`, `speckit plan`, `speckit tasks`, `speckit implement`

## Auto-Detection Logic

When invoked without a specific phase, check for existing files:

| Files Present | Start Phase |
|---------------|-------------|
| None | SPECIFY |
| spec.md only | PLAN |
| spec.md + plan.md | TASKS |
| spec.md + plan.md + tasks.md | IMPLEMENT |

## Phase Details

### SPECIFY → spec.md

Create `spec.md` with:
- Feature overview
- User stories (P1/P2/P3 priority)
- Acceptance criteria
- Success criteria (measurable)

**Ask clarifying questions** if requirements are unclear.

### PLAN → plan.md

Create `plan.md` with:
- Technical context (stack, architecture)
- Data model changes
- API contracts
- Security considerations

Reference `spec.md` for requirements.

### TASKS → tasks.md

Create `tasks.md` with:
- Phases: Setup → Foundation → User Stories → Polish
- Each task: `- [ ] T001 Description`
- Mark parallelizable tasks with `[P]`

Reference `plan.md` for implementation details.

### IMPLEMENT → code

Execute tasks from `tasks.md`:
1. Find first unchecked task
2. **Use test-driven-development skill** to implement
3. Run tests
4. Mark complete: `[ ]` → `[X]`
5. Repeat

**Stop and ask** if blocked or unclear.

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

**Starting fresh:**
```
User: speckit I want to add user authentication
→ Creates spec.md, asks clarifying questions
→ Proceeds to plan.md
→ Proceeds to tasks.md
→ Starts implementation
```

**Continuing:**
```
User: speckit
→ Detects existing files
→ Continues from current phase
```

---

**User's Request:**

$ARGUMENTS
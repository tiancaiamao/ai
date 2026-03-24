**Break down complex work before diving in** — Plan → Execute → Verify iteratively.

### Task Type Detection

Before decomposing, detect task type:

**Simple Tasks** (handle directly):
- Single file change
- Clear requirement, no ambiguity
- Single logical step
- < 15 min estimated

**Complex Tasks** (use structured approach):
- Multiple files/components
- Unknown dependencies
- Ambiguous requirements
- >3 logical steps
- > 30 min estimated

### Routing Logic

```
Task Request
    ↓
[Check for existing artifacts]
    ├─ tasks.md exists?
    │   └─ Yes → /skill:auto-execute (direct execution)
    │
    ├─ Clear feature requirement?
    │   └─ Yes → /skill:speckit (spec → plan → tasks → auto-execute)
    │
    ├─ Exploratory/unclear?
    │   └─ Yes → /skill:explore or /skill:brainstorming
    │               ↓
    │           Clarify requirements
    │               ↓
    │           /skill:speckit (generate tasks.md)
    │               ↓
    │           /skill:auto-execute
    │
    └─ Conversation revealed clear task?
        └─ Yes → Use llm_context_update (task tracking)
                     ↓
                 /skill:speckit (create tasks.md)
                     ↓
                 /skill:auto-execute
```

**Key Principles**:
- If tasks.md exists and approved → use auto-execute directly
- If starting new feature work → use speckit to create tasks.md first
- If requirements unclear → explore/brainstorm, then speckit
- If evolving from conversation → track context, then speckit

### When to Decompose

Use decomposition when task has:
- Multiple files/components
- Unknown dependencies
- Ambiguous requirements
- >3 logical steps

### Simple Framework

1. **Understand first** — Read relevant files, clarify ambiguity
2. **Plan briefly** — List main components + dependencies
3. **Execute in order** — Tackle independent parts in parallel if possible
4. **Verify each step** — Don't batch-fix at end

**Or use structured workflows**:
- `/skill:speckit` - For feature development (spec → plan → tasks)
- `/skill:auto-execute` - For executing existing tasks.md
- `/skill:brainstorming` - For exploring unclear requirements
- `/skill:explore` - For understanding codebases

For detailed decomposition patterns and examples, check available skills.

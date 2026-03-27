---
id: feature
name: Feature Development
description: Develop a new feature from spec to ship
phases: [spec, plan, implement, test, ship]
complexity: medium
estimated_tasks: 4-8
---

# Feature Development Workflow

## Overview

Feature workflow guides you from idea to shipped code with proper checkpoints.

## Phase 1: Spec

### Goals
- Clarify what we're building
- Define success criteria
- Identify constraints

### Actions

1. Understand the ask
   - What problem does this solve?
   - Who is the user?
   - What's the value?

2. Define scope
   - What's in scope?
   - What's explicitly out of scope?

3. Write SPEC.md

### Output

Create `SPEC.md`:

```markdown
# Feature: [Name]

## Summary
[1-2 sentence description]

## Motivation
[Why are we doing this?]

## User Stories
- As a [user], I want [goal] so that [benefit]
- ...

## Requirements
- [ ] [requirement 1]
- [ ] [requirement 2]

## Out of Scope
- [ ] [explicitly not doing]

## Success Criteria
- [ ] [criterion 1]
- [ ] [criterion 2]

## Technical Notes
[Any implementation considerations]
```

### Review Criteria
- [ ] Clear user value?
- [ ] Scope well-defined?
- [ ] Success criteria measurable?

---

## Phase 2: Plan

### Goals
- Break down into actionable tasks
- Estimate effort
- Identify dependencies

### Actions

1. Explore codebase
   - Find relevant files
   - Understand existing patterns
   - Identify integration points

2. Create plan.md

### Output

Create `PLAN.md`:

```markdown
# Plan: [Feature Name]

## Tasks

### TASK-1: [Task Name]
**Files:** `src/xxx.go`
**Description:** [What to do]
**Effort:** [small/medium/large]

### TASK-2: [Task Name]
...

## Implementation Order
1. TASK-1
2. TASK-2
...

## Risks
- [risk 1] → [mitigation]

## Testing Strategy
[How to verify the feature works]
```

### Review Criteria
- [ ] All requirements addressed?
- [ ] Dependencies clear?
- [ ] Effort reasonable?

---

## Phase 3: Implement

### Goals
- Execute plan tasks
- Follow existing patterns
- Write clean, tested code

### Actions

1. Execute tasks in order
2. Run tests after each task
3. Update plan progress

### Output

- Modified files
- Tests added
- `PLAN.md` updated with progress

### Review Criteria
- [ ] Code follows patterns?
- [ ] Tests pass?
- [ ] No obvious bugs?

---

## Phase 4: Test

### Goals
- Verify all requirements met
- Run full test suite
- Manual testing if needed

### Actions

1. Run test suite
2. Verify each requirement
3. Check edge cases
4. Performance testing if relevant

### Output

Create `test-results.md`:

```markdown
# Test Results

## Requirements Verification
- [x] [requirement 1] → verified
- [x] [requirement 2] → verified

## Test Suite
```
[test output]
```

## Manual Tests
- [ ] [manual test 1] → passed
- [ ] [manual test 2] → passed

## Notes
[Any issues found]
```

### Review Criteria
- [ ] All requirements tested?
- [ ] No regressions?
- [ ] Edge cases covered?

---

## Phase 5: Ship

### Goals
- Clean commit history
- PR with good description
- Documentation updated

### Actions

1. Squash commits if messy
2. Write good PR description
3. Update relevant docs
4. Link to any issues

### PR Template

```markdown
## Summary
[Brief description of what this does]

## Changes
- [change 1]
- [change 2]

## Testing
- [x] Tests pass
- [x] Manual verification complete

## Screenshots (if UI)
[Before/After]

## Related Issues
Closes #123
```

### Review Criteria
- [ ] PR description complete?
- [ ] Tests documented?
- [ ] Ready to merge?
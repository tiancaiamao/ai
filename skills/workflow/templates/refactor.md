---
id: refactor
name: Refactor
description: Restructure code without changing behavior
phases: [assess, plan, execute, verify, ship]
complexity: medium
estimated_tasks: 3-6
---

# Refactor Workflow

## Overview

Refactor workflow ensures safe code restructuring with behavioral preservation.

## Core Principle

> **Always refactor in small, verifiable steps. If tests break, you broke behavior.**

## Phase 1: Assess

### Goals
- Understand what needs refactoring
- Identify code smells
- Measure current state

### Actions

1. Inventory problem areas
   - Large functions (>50 lines)
   - Deep nesting (>3 levels)
   - Duplicated code
   - Poor naming
   - Tight coupling

2. Measure
   - Test coverage in target area
   - Complexity metrics
   - Dependencies

3. Document findings

### Output

Create `assess.md`:

```markdown
# Refactor Assessment: [Target]

## Code Smells Identified
1. **[smell type]** in [file:line]
   - Why it's a problem
   - Suggested fix

## Current Metrics
- Functions: [count]
- Test coverage: [%]
- Cyclomatic complexity: [avg/max]

## Risk Assessment
- **High risk:** [areas with no tests]
- **Medium risk:** [areas with some tests]
- **Low risk:** [well-tested areas]
```

### Review Criteria
- [ ] Problem areas identified?
- [ ] Test coverage assessed?
- [ ] Risk level reasonable?

---

## Phase 2: Plan

### Goals
- Sequence refactoring steps
- Define rollback points
- Set success criteria

### Actions

1. Sequence changes
   - Safe changes first (rename, extract)
   - Risky changes last (behavioral)
   - Independent changes can parallelize

2. Define checkpoints
   - After each step: tests pass
   - Document working state

### Output

Create `refactor-plan.md`:

```markdown
# Refactor Plan: [Target]

## Steps

### Step 1: [Safe Change]
**Type:** rename/extract/move
**Risk:** low
**Rollback:** git checkout

### Step 2: [Medium Change]
...

## Checkpoints
1. After Step 1 → tests pass
2. After Step 2 → tests pass
3. After Step N → verify full suite

## Success Criteria
- [ ] All tests pass
- [ ] No behavioral change
- [ ] Code is cleaner
```

### Review Criteria
- [ ] Steps sequenced safely?
- [ ] Rollback defined?
- [ ] Testable checkpoints?

---

## Phase 3: Execute

### Goals
- Execute steps in order
- Run tests after each
- Preserve behavior

### Actions

1. For each step:
   - Make the change
   - Run targeted tests
   - If fail: rollback, reassess
   
2. Commit after each successful step

### Techniques

**Rename Variable/Function:**
```bash
# Safe: IDE refactor or manual
# 1. Find all usages
grep -rn "oldName"
# 2. Replace all
sed -i 's/oldName/newName/g' $(grep -rl oldName)
# 3. Test
go test ./...
```

**Extract Function:**
```go
// Before
func process(data []byte) {
    // 100 lines of logic
}

// After
func process(data []byte) {
    result := transform(data)
    validate(result)
    store(result)
}
```

**Move to Package:**
```bash
# 1. Copy files to new location
cp -r old/pkg new/pkg
# 2. Update imports
find . -name "*.go" -exec sed -i 's/old\/pkg/new\/pkg/g' {}
# 3. Remove old
rm -r old/pkg
# 4. Test
go test ./...
```

### Review Criteria
- [ ] Tests pass after each step?
- [ ] No behavior change?
- [ ] Code is cleaner?

---

## Phase 4: Verify

### Goals
- Full test suite passes
- Performance not degraded
- Code quality improved

### Actions

1. Run full test suite
2. Check metrics improved
3. Manual code review

### Output

Create `verify.md`:

```markdown
# Refactor Verification

## Test Results
```
[full test output]
```

## Metrics Comparison

| Metric | Before | After |
|--------|--------|-------|
| LOC | X | Y |
| Complexity | X | Y |
| Coverage | X% | Y% |

## Notes
[Any issues or observations]
```

### Review Criteria
- [ ] All tests pass?
- [ ] Metrics improved?
- [ ] No regressions?

---

## Phase 5: Ship

### Goals
- Clean commit history
- Document what changed
- Share learnings

### Actions

1. Squash if needed
2. Write descriptive commit
3. Update docs if needed

### Commit Message

```
refactor: [what changed]

- [specific change 1]
- [specific change 2]

Improves code quality without changing behavior.
```
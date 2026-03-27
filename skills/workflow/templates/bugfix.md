---
id: bugfix
name: Bug Fix
description: Fix bugs with root-cause analysis
phases: [triage, fix, verify, ship]
complexity: low
estimated_tasks: 2-4
---

# Bug Fix Workflow

## Overview

Bugfix workflow focuses on **root-cause analysis** before jumping to solutions. Most bugs are symptoms of deeper issues.

## Phase 1: Triage

### Goals
- **Reproduce** the issue consistently
- **Identify** root cause (not just symptoms)
- **Document** findings and impact

### Actions

1. Gather context
   - User report / error message
   - When did it start?
   - What changed recently?

2. Reproduce the bug
   - Create minimal reproduction case
   - Note exact steps, inputs, expected vs actual

3. Root cause analysis
   - Why does it happen?
   - What's the code path?
   - Is this a symptom of something bigger?

### Output

Create `triage.md` in artifact directory:

```markdown
# Bug Triage: [brief title]

## Issue Summary
[1-2 sentence description]

## Reproduction
**Steps:**
1. 
2. 
3.

**Expected:** 
**Actual:** 

## Root Cause
[Detailed explanation of WHY it happens]

## Impact
- Severity: [critical/high/medium/low]
- Affected users: [who/what]
- Workaround: [if any]

## Fix Direction
[Brief note on potential fix approach]
```

### Review Criteria
- [ ] Can reproduce consistently?
- [ ] Root cause identified?
- [ ] Not just treating symptoms?

---

## Phase 2: Fix

### Goals
- Implement fix for **root cause**
- Add regression tests
- Ensure no side effects

### Actions

1. Plan the fix
   - Fix the root cause, not symptoms
   - Consider edge cases
   - Think about similar issues elsewhere

2. Implement
   - Make the minimal change needed
   - Follow existing code patterns
   - Add comments for tricky logic

3. Test
   - Verify fix works
   - Run existing tests
   - Add regression test

### Output

- Modified files with fix
- New or updated tests
- Update `triage.md` with fix notes

### Review Criteria
- [ ] Fix addresses root cause?
- [ ] Tests added/updated?
- [ ] No regressions?

---

## Phase 3: Verify

### Goals
- Full test suite passes
- Fix verified in realistic scenarios
- Edge cases handled

### Actions

1. Run test suite
   ```bash
   go test ./...  # or your test command
   ```

2. Manual verification
   - Follow original reproduction steps
   - Verify fix works

3. Check edge cases
   - What about null/undefined?
   - What about concurrent access?
   - What about large inputs?

### Output

Create `verify-results.md`:

```markdown
# Verification Results

## Test Suite
```
[test output]
```

## Manual Verification
- [ ] Bug reproduction steps now work correctly
- [ ] Edge cases handled

## Notes
[Any additional verification notes]
```

### Review Criteria
- [ ] All tests pass?
- [ ] Manual verification complete?
- [ ] Edge cases considered?

---

## Phase 4: Ship

### Goals
- Commit with clear message
- Create PR if applicable
- Update any relevant docs

### Actions

1. Commit
   ```bash
   git add -A
   git commit -m "fix: [brief description] (#issue-number)"
   ```

2. Push and create PR
   ```bash
   git push -u origin HEAD
   # Create PR with bugfix template
   ```

3. Update issue
   - Link to PR
   - Mark as resolved

### Review Criteria
- [ ] Commit message follows convention?
- [ ] PR created with description?
- [ ] Issue updated?

---

## Commit Conventions

```
fix: resolve login timeout after 30s
fix: handle nil pointer in user lookup
fix: correct off-by-one in pagination
```

Format: `fix: [what] [where if needed]`
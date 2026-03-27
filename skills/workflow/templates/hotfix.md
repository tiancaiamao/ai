---
id: hotfix
name: Hotfix
description: Emergency production fix with minimal ceremony
phases: [identify, fix, deploy]
complexity: minimal
estimated_tasks: 1-2
---

# Hotfix Workflow

## Overview

Hotfix is for **emergency production issues**. Minimal ceremony, fast execution.

## Core Principle

> **Fix fast, but don't break more. If unsure, rollback.**

## When to Use Hotfix

- Production is down or severely degraded
- Security vulnerability exposed
- Data integrity at risk
- Customer-facing critical bug

If it's not blocking production, use `bugfix` workflow instead.

## Phase 1: Identify

### Goals
- Confirm the issue
- Understand impact
- Plan minimal fix

### Actions

1. Verify the issue
   - Check monitoring/alerting
   - Reproduce if possible
   - Confirm severity

2. Assess scope
   - What's affected?
   - How many users?
   - Can we rollback?

3. Plan minimal fix
   - What's the smallest change that resolves it?
   - Can we do a temporary fix?

### Time Box

**15 minutes maximum** for identify. If you can't identify the issue:
1. Escalate immediately
2. Consider rollback
3. Get more eyes on it

### Output

```markdown
# Hotfix: [Issue]

## Issue
[Brief description]

## Impact
- Severity: [P0/P1]
- Users affected: [X]
- Duration: [ongoing since X]

## Root Cause (if known)
[Quick assessment]

## Fix Plan
1. [step 1]
2. [step 2]
```

---

## Phase 2: Fix

### Goals
- Implement minimal fix
- Test quickly
- Prepare for deploy

### Actions

1. Make the fix
   - Smallest change possible
   - No new features
   - No refactoring

2. Quick test
   - Does it fix the issue?
   - Do basic tests pass?

3. Prepare deploy package

### Time Box

**30 minutes maximum**. If fix isn't ready:
- Consider rollback
- Deploy partial fix
- Escalate

### Review Criteria
- [ ] Fix addresses issue?
- [ ] No obvious regressions?
- [ ] Ready to deploy?

---

## Phase 3: Deploy

### Goals
- Deploy fix to production
- Verify resolution
- Document post-mortem

### Actions

1. Deploy
   - Follow deploy procedure
   - Monitor during deploy
   - Watch for new issues

2. Verify
   - Is issue resolved?
   - Are new errors appearing?
   - Monitoring healthy?

3. Document
   - Create post-mortem
   - Schedule proper follow-up
   - Clean up any temp code

### Post-Mortem Template

```markdown
# Post-Mortem: [Incident]

## Summary
[Brief description of what happened and impact]

## Timeline
- HH:MM - Issue detected
- HH:MM - Incident declared
- HH:MM - Fix deployed
- HH:MM - Resolved

## Root Cause
[What caused the issue]

## Resolution
[How we fixed it]

## Action Items
- [ ] [Action 1] - [Owner] - [Date]
- [ ] [Action 2] - [Owner] - [Date]

## Lessons Learned
- [Lesson 1]
- [Lesson 2]

## Follow-up
- Proper fix for [underlying issue]
- Monitoring improvements
```

### Review Criteria
- [ ] Issue resolved?
- [ ] Post-mortem scheduled?
- [ ] Action items assigned?
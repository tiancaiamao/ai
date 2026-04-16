---
id: hotfix
name: Hotfix
description: Emergency production fix with minimal ceremony
phases: [implement]
complexity: minimal
estimated_tasks: 1-2
skills:
  implement: implement
---

# Hotfix Workflow

## Overview

Emergency fix. Minimal process — just fix it fast and ship.
All other workflows have more process; hotfix deliberately has less.

## Phase: Implement

**Skill:** `implement` (simplified)

1. Understand the emergency (from user)
2. Find the root cause quickly
3. Write a failing test that reproduces the issue
4. Fix it
5. Verify test passes
6. Commit and push

**Simplifications for hotfix:**
- Skip two-stage review if user confirms urgency
- Skip full test suite — only run tests related to the fix
- Skip sub-agent delegation — implement directly for speed
- Single commit, push immediately

## Hotfix-Specific Rules

- **Speed over process** — fix first, add process later
- **Minimal scope** — fix only the immediate issue
- **Test the fix** — even in emergencies, write a test
- **Follow-up** — after hotfix, consider a bugfix workflow for proper cleanup

## Commit Convention

```
fix(scope): resolve [issue] (hotfix)
```
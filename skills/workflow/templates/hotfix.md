# Hotfix Workflow

## Overview

Emergency fix. Minimal process — just fix it fast and ship.
All other workflows are better suited for non-emergency work.

**Core rule:** Ship the fix fast. Minimize process. Fix can be improved later.

## Phase Sequence

```
implement (only)
```

### Phase 1: Implement

**Skill:** `implement`

1. Identify the fix (may be 1-line)
2. Write a test if feasible
3. Apply the fix
4. Verify locally
5. Commit and push

No gate — just do it.

**Output:** Git commit

## Hotfix-Specific Rules

- **Speed over perfection** — get the fix out
- **Minimal change** — don't improve surrounding code
- **Follow up** — after the emergency, consider a bugfix workflow to improve the fix
- **No refactoring** — not the time for it

## Commit Convention

```
fix(scope): hotfix [brief description]
```

## After Hotfix

Consider creating a bugfix or refactor workflow to:
- Add proper tests
- Clean up the emergency fix
- Address root cause more thoroughly
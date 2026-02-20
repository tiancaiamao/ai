---
name: speckit-specify
description: INTERNAL - Used by speckit. Do not invoke directly. Creates spec.md from requirements.
license: MIT
---

# Specification Phase

⚠️ **This is an internal phase of speckit. Use `speckit` command instead of invoking this directly.**

## Purpose

Create `spec.md` - a clear, testable specification of what we're building.

## Output

Create `spec.md` with:

```markdown
# Feature: [Feature Name]

## Overview
Brief description of the feature.

## User Stories

### P1 (Must Have)
- As a [user], I want [action], so that [benefit].

### P2 (Should Have)
- ...

### P3 (Nice to Have)
- ...

## Acceptance Criteria
- [ ] Criterion 1
- [ ] Criterion 2

## Success Criteria
- Measurable outcome 1
- Measurable outcome 2
```

## Process

1. **Gather requirements** from user
2. **Ask clarifying questions** if unclear
3. **Write spec.md**
4. **Get user approval** before proceeding

---

**User's Request:**

$ARGUMENTS
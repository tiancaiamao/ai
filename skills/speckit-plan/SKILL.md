---
name: speckit-plan
description: INTERNAL - Used by speckit. Do not invoke directly. Creates plan.md from spec.md.
license: MIT
---

# Planning Phase

⚠️ **This is an internal phase of speckit. Use `speckit` command instead of invoking this directly.**

## Purpose

Create `plan.md` - a technical implementation plan based on `spec.md`.

## Prerequisites

- `spec.md` must exist

## Output

Create `plan.md` with:

```markdown
# Implementation Plan: [Feature Name]

## Technical Context
- Stack: [technologies]
- Architecture: [pattern]

## Data Model
[Schema changes, new types, etc.]

## API Contracts
[New endpoints, changes to existing ones]

## Components
[New components, modifications]

## Security Considerations
[Auth, validation, etc.]

## Implementation Order
1. Foundation
2. Core feature
3. Polish
```

## Process

1. **Read spec.md** for requirements
2. **Explore codebase** to understand existing patterns
3. **Write plan.md**
4. **🔷 PHASE GATE**: Present key decisions, discuss trade-offs, get explicit approval

**Do NOT auto-proceed to task generation without user confirmation.**

---

**User's Request:**

$ARGUMENTS
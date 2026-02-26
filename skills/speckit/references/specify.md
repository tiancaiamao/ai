# Specification Phase

## Purpose

Create `spec.md` - a clear, testable specification of what we're building.

## Process

1. **Gather requirements** from $ARGUMENTS
2. **Ask clarifying questions** - dig deep, be curious
3. **Write spec.md** with the structure below
4. **🔷 PHASE GATE**: Present summary, get explicit approval before proceeding

**Do NOT auto-proceed to planning without user confirmation.**

## spec.md Template

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

## Phase Gate Template

```
---
📋 **spec.md created!** Here's the summary:

[2-3 bullet summary of what we're building]

**Key decisions:**
- [Decision 1]
- [Decision 2]

**Questions for you:**
- [Any open questions]

Ready to proceed to planning? Any changes needed?
---
```

## Guidelines for Gathering Requirements

### Ask Open-Ended Questions
- "How do you envision this feature working?"
- "Who are the primary users?"
- "What problem does this solve?"

### Dig Deeper
- "What happens if [edge case]?"
- "How should we handle errors?"
- "What's the expected scale/performance?"

### Prioritize
- Group features by P1 (must have), P2 (should have), P3 (nice to have)
- Focus on P1 first

### Make It Testable
- Write clear acceptance criteria
- Define measurable success criteria
- Avoid vague statements like "improve UX"

## Common Mistakes

- ❌ Skipping clarifying questions and making assumptions
- ❌ Writing acceptance criteria that can't be tested
- ❌ Including implementation details in the spec
- ❌ Not prioritizing user stories
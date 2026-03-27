---
id: spike
name: Spike
description: Research and exploration
phases: [research, document, present]
complexity: low
estimated_tasks: 1-2
---

# Spike Workflow

## Overview

Spike is for **research, exploration, and prototyping**. No production code required.

## Core Principle

> **Spikes are time-boxed. If you hit the limit, document what you learned and stop.**

## Phase 1: Research

### Goals
- Understand the problem/technology
- Evaluate options
- Form recommendations

### Time Box

**2 hours maximum** for most spikes. If you need more:
1. Document what you learned
2. Get approval to continue
3. Or create follow-up spike

### Actions

1. Define research questions
   - What are we trying to learn?
   - What decisions will this inform?

2. Explore sources
   - Read docs
   - Search code
   - Run experiments
   - Talk to experts

3. Take notes
   - What works?
   - What doesn't?
   - Surprises?

### Output

Create `research.md`:

```markdown
# Spike: [Topic]

## Research Questions
1. [Question 1]
2. [Question 2]

## Findings

### [Area 1]
**What I learned:**
- [finding 1]
- [finding 2]

**Surprises:**
- [surprise 1]

### [Area 2]
...

## Options Evaluated

| Option | Pros | Cons | Recommendation |
|--------|------|------|----------------|
| A | ... | ... | ✓ |
| B | ... | ... | |

## Recommendations
1. [recommendation 1]
2. [recommendation 2]

## Open Questions
- [question 1] → [how to answer]
- [question 2] → [how to answer]

## Time Spent
[1.5 hours]

## Next Steps
- [ ] [Follow-up action 1]
- [ ] [Follow-up action 2]
```

### Review Criteria
- [ ] Research questions answered?
- [ ] Options documented?
- [ ] Recommendations clear?

---

## Phase 2: Document

### Goals
- Capture learnings
- Make it actionable
- Enable future reference

### Actions

1. Refine research notes
2. Create decision record if needed
3. Add code examples if relevant

### Output

Update `research.md` or create `DECISION.md`:

```markdown
# Decision: [Topic]

## Context
[What problem are we solving?]

## Decision
[What we decided to do]

## Rationale
[Why this choice over alternatives]

## Consequences
**Positive:**
- [benefit 1]

**Negative:**
- [trade-off 1]

## Status
Decided: [date]
Review: [when to revisit]
```

### Review Criteria
- [ ] Learnings captured?
- [ ] Actionable output?
- [ ] Future-us can understand?

---

## Phase 3: Present

### Goals
- Share findings with team
- Enable decision making
- Archive for future reference

### Actions

1. Summarize for stakeholders
2. Answer questions
3. File artifacts appropriately

### Output

Brief summary for user:

```
## Spike Complete: [Topic]

### Key Findings
- [finding 1]
- [finding 2]

### Recommendation
[One sentence recommendation]

### Details
See: .workflow/spikes/[name]/research.md

### Next Steps
- [ ] [Action based on spike]
```

### Review Criteria
- [ ] Team informed?
- [ ] Artifacts filed?
- [ ] Actions defined?
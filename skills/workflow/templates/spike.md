# Spike Workflow

## Overview

Time-boxed research. Learn something, document findings, present to user.
No production code required.

**Core rule:** Spikes are time-boxed. Hit the limit? Document what you
learned and stop.

## Phase Sequence

```
brainstorm → document
```

### Phase 1: Brainstorm

**Skill:** `brainstorm`

Define research questions:
1. What are we trying to learn?
2. What decisions will this inform?
3. What's the time limit? (default: 2 hours)

Explore freely — read docs, search code, run experiments.

**Gate:** Research questions defined and exploration done.

**Output:** Research notes (use `workflow-ctl note` to track progress)

### Phase 2: Document

**Skill:** `spec` (repurposed as documentation)

Write up findings:

```markdown
# Spike: [Topic]

## Research Questions
1. [question]
2. [question]

## Findings
- [finding 1]
- [finding 2]

## Recommendation
[One sentence]

## Trade-offs
| Option | Pros | Cons |
|--------|------|------|
| A | ... | ... |
| B | ... | ... |

## Next Steps
- [ ] [action based on spike]
```

If the spike leads to a decision, create a `DECISION.md`:

```markdown
# Decision: [Topic]
## Context
## Decision
## Rationale
## Consequences
```

**Gate:** User accepts the documentation.

**Output:** `research.md` in artifact directory

## Spike-Specific Rules

- **Time-boxed** — 2 hours max by default; get approval to extend
- **No production code** — experiments and prototypes only
- **Document everything** — future-you needs to understand why
- **Actionable output** — end with a recommendation or decision
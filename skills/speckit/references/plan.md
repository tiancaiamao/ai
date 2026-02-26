# Planning Phase

## Purpose

Create `plan.md` - a technical implementation plan based on `spec.md`.

## Prerequisites

- `spec.md` must exist
- Spec must be approved by user

## Process

1. **Read spec.md** for requirements
2. **Explore codebase** to understand existing patterns
3. **Write plan.md** with the structure below
4. **🔷 PHASE GATE**: Present plan, discuss trade-offs, get approval

**Do NOT auto-proceed to task generation without user confirmation.**

## plan.md Template

```markdown
# Implementation Plan: [Feature Name]

## Technical Context
- Stack: [technologies in use]
- Architecture: [relevant patterns, e.g., MVC, microservices]
- Dependencies: [any new dependencies needed]

## Data Model
[Schema changes, new types, database migrations, etc.]

## API Contracts
[New endpoints, changes to existing ones, request/response formats]

## Components
[New components, modifications to existing ones, file locations]

## Security Considerations
[Auth, validation, input sanitization, rate limiting, etc.]

## Implementation Order
1. Foundation [setup, data models, base infrastructure]
2. Core feature [main business logic]
3. Integration [connecting components]
4. Polish [error handling, logging, edge cases]
```

## Phase Gate Template

```
---
📐 **plan.md created!** Key technical decisions:

**Architecture:**
- [Key architectural choice and rationale]

**Approach:**
- [Implementation approach explanation]

**Trade-offs considered:**
- [Trade-off 1]: chose X because...
- [Trade-off 2]: chose Y because...

**Scope:**
- [What's included]
- [What's NOT included]

Does this approach work for you? Any concerns before we break this into tasks?
---
```

## Guidelines for Creating the Plan

### Explore the Codebase
- Look for similar features to understand patterns
- Identify existing utilities/helpers you can reuse
- Check data model and API conventions

### Document Technical Decisions
- Explain *why* you chose a specific approach
- Mention alternatives you considered
- Note trade-offs explicitly

### Be Specific
- List exact file locations for new components
- Define clear data structures
- Specify API endpoints with methods and paths

### Consider Security Early
- Authentication requirements
- Input validation needs
- Authorization checks
- Rate limiting if applicable

## Common Mistakes

- ❌ Skipping codebase exploration and reinventing the wheel
- ❌ Being vague about implementation details
- ❌ Not considering security implications
- ❌ Including implementation that's out of scope
- ❌ Forgetting to mention external dependencies
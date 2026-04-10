# ADR 002: Code-Driven Workflow Engine

**Status:** Accepted
**Date:** 2024-03-01
**Context:** Workflow system implementation

## Context

When implementing the workflow system, we needed to choose between:

1. **Prompt-driven workflow** - LLM controls workflow decisions
2. **Code-driven workflow** - Code state machine controls workflow

## Decision

Chose code-driven workflow engine using Go state machine.

## Rationale

### Why Code-Driven?

1. **Reliability**
   - Deterministic control flow (no LLM hallucinations)
   - Predictable behavior across runs
   - No unexpected workflow state changes

2. **Performance**
   - No extra LLM calls for workflow decisions
   - Faster task scheduling
   - Lower cost

3. **Debuggability**
   - Clear code paths for workflow logic
   - Easy to trace execution
   - Can unit test workflow without LLM

4. **Testability**
   - Unit tests for workflow state transitions
   - Integration tests for task scheduling
   - No dependency on LLM for workflow logic

### Workflow Design

```
YAML Template
    ↓
  Parse
    ↓
  Create Tasks (from phases)
    ↓
  Schedule Tasks (by dependencies)
    ↓
  Workers Claim & Execute
    ↓
  Update Task State
    ↓
  Next Task (if dependencies satisfied)
```

### Disadvantages

1. **Less Flexible**
   - Can't self-modify workflow at runtime
   - Requires code changes to modify workflow logic
   - Template changes need careful testing

2. **More Code**
   - Need to implement state machine
   - Need to handle edge cases in code
   - More complex than prompt-based approach

### Rejected Alternative: Prompt-Driven Workflow

**Rejected because:**
- LLM hallucinations could cause incorrect task scheduling
- Unreliable control flow
- Harder to debug (non-deterministic)
- Slower (extra LLM calls)
- More expensive

### Alternative: Hybrid Approach

**Considered but rejected:**
- LLM for high-level decisions, code for execution
- Still introduces unreliability into control flow
- Added complexity without clear benefit

## Consequences

### Positive

- Reliable task scheduling
- Better performance (no extra LLM calls)
- Easier to debug (deterministic)
- Testable without LLM
- Clear workflow logic

### Negative

- Less flexible (can't self-modify at runtime)
- Requires code changes for workflow logic updates
- More code to maintain

### Mitigations

- Use YAML templates for workflow definition (easy to modify)
- Add offline optimization using session-analyzer
- Provide human-in-the-loop checkpoints
- Keep workflow logic simple and clear
- Good test coverage for workflow state transitions

## Implementation

### Core Components

1. **Orchestrate Runtime** (`skills/workflow/orchestrate/runtime.go`)
   - Load YAML templates
   - Create tasks from phases
   - Schedule tasks by dependencies
   - Manage task state

2. **API** (`skills/workflow/orchestrate/api.go`)
   - Worker interface for claiming/completing tasks
   - Human review interface
   - Task query interface

3. **Storage** (`skills/workflow/orchestrate/storage.go`)
   - File-based task state persistence
   - Thread-safe operations

### Task States

```
pending → claimed → in_progress → completed
    ↓           ↓           ↓
  blocked   failed     failed
```

### Dependency Management

```yaml
phases:
  - id: explore
    subject: "Explore codebase"
  - id: plan
    subject: "Create plan"
    blocked_by: [explore]  # Requires explore to complete
  - id: implement
    subject: "Implement"
    blocked_by: [plan]  # Requires plan to complete
```

## References

- Related: ADR 001 (RPC-First Design)
- Related: ADR 003 (Tmux for Subagents)
- Workflow templates: `skills/workflow/templates/`
- Orchestrate implementation: `skills/workflow/orchestrate/`
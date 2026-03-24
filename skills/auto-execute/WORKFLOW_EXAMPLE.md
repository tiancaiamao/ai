# Complete Workflow Example

This example shows a complete end-to-end workflow using speckit and auto-execute.

## Scenario

You want to build a simple CLI tool that greets users with a personalized message.

## Step 1: Specify - Create spec.md

```markdown
# Feature Specification: Personal Greeting CLI

## Purpose
Create a CLI tool that greets users with personalized messages.

## Requirements
1. Accept name as command-line argument
2. Output "Hello, <name>!" message
3. Support optional greeting parameter

## Success Criteria
- ✅ Binary compiles successfully
- ✅ `./greet --name=Alice` outputs "Hello, Alice!"
- ✅ `./greet --name=Alice --greeting=Hi` outputs "Hi, Alice!"
```

## Step 2: Plan - Create plan.md

```markdown
# Implementation Plan

## Architecture
- Go 1.24.0
- Single binary: cmd/greet/main.go
- Use standard library (no external dependencies)

## Phases
1. Project setup (10 min)
2. Core implementation (20 min)
3. Testing (15 min)
```

## Step 3: Tasks - Create tasks.md

```markdown
# Greeting CLI Tasks

## Setup
- [ ] T001: Create project structure
  **Acceptance Criteria**:
  - Create cmd/greet/main.go
  - Initialize go mod

## Implementation
- [ ] T002: Implement argument parsing
  **Acceptance Criteria**:
  - Parse --name flag (required)
  - Parse --greeting flag (optional, default "Hello")

- [ ] T003: Implement greeting logic
  **Acceptance Criteria**:
  - Format greeting message
  - Print to stdout

## Testing
- [ ] T004: Add unit tests
  **Acceptance Criteria**:
  - Test greeting formatting
  - Run `go test` - all pass

- [ ] T005: Manual verification
  **Acceptance Criteria**:
  - Build binary: `go build ./cmd/greet`
  - Test: `./greet --name=Alice` outputs "Hello, Alice!"
  - Test: `./greet --name=Alice --greeting=Hi` outputs "Hi, Alice!"
```

## Step 4: Execute - Use auto-execute

```bash
# Run automated execution
/skill:auto-execute
```

### Execution Log

```
=== Auto-Execute Starting ===
Found 5 tasks in tasks.md

[T001] Create project structure
  → Worker agent implementing...
  → Task-checker verifying...
  ✅ Approved - Marking as done

[T002] Implement argument parsing
  → Worker agent implementing...
  → Task-checker verifying...
  ✅ Approved - Marking as done

[T003] Implement greeting logic
  → Worker agent implementing...
  → Task-checker verifying...
  ✅ Approved - Marking as done

[T004] Add unit tests
  → Worker agent implementing...
  → Task-checker verifying...
  ✅ Approved - Marking as done

[T005] Manual verification
  → Worker agent implementing...
  → Task-checker verifying...
  ✅ Approved - Marking as done

=== All Tasks Complete ===
Total: 5, Done: 5, Failed: 0
```

## Step 5: Verify - Run tests

```bash
go test ./...
# PASS: TestGreeting (0.00s)
# PASS
```

## Alternative: Stop and Review

If you want to review progress after certain tasks:

```bash
/skill:auto-execute stop_after=2
```

Output:
```
=== Auto-Execute Stopping (stop_after=2) ===
Completed 2 of 5 tasks:
  ✅ T001: Create project structure
  ✅ T002: Implement argument parsing

Tasks remaining:
  - T003: Implement greeting logic
  - T004: Add unit tests
  - T005: Manual verification

Continue? Run /skill:auto-execute again
```

## Alternative: Dry Run Preview

Before executing, preview what will happen:

```bash
/skill:auto-execute dry_run=true
```

Output:
```
[DRY RUN] Would execute:
  - T001: Create project structure
  - T002: Implement argument parsing
  - T003: Implement greeting logic
  - T004: Add unit tests
  - T005: Manual verification
```

## Error Handling Example

If a task fails:

```
[T002] Implement argument parsing
  → Worker agent implementing...
  → Task-checker verifying...
  ❌ Rejected - Tests failing
  → Retrying (1/3)...

  → Worker agent fixing...
  → Task-checker verifying...
  ❌ Rejected - Still failing
  → Retrying (2/3)...

  → Worker agent fixing...
  → Task-checker verifying...
  ❌ Rejected - Max retries exceeded

=== Execution Stopped ===
Task T002 failed: Tests failing - expected ParseFlags not found
Returning to user for manual intervention
```

### Manual Fix

```bash
# Review the issue
# Fix the problem manually
vim cmd/greet/main.go

# Update task status to done once fixed
# [x] T002: Implement argument parsing

# Continue execution
/skill:auto-execute
```

## Progress Monitoring

At any time, check progress:

```
/task progress
```

Output:

```markdown
## Auto-Execute Progress
- Phase: executing
- Total: 5
- Done: 2
- In Progress: T003

Completed:
  ✅ T001: Create project structure
  ✅ T002: Implement argument parsing

Current:
  🔄 T003: Implement greeting logic

Remaining:
  - T004: Add unit tests
  - T005: Manual verification
```

## Complete tasks.md After Execution

```markdown
# Greeting CLI Tasks

## Setup
- [x] T001: Create project structure

## Implementation
- [x] T002: Implement argument parsing
- [x] T003: Implement greeting logic

## Testing
- [x] T004: Add unit tests
- [x] T005: Manual verification
```

## Summary

This workflow demonstrates:

1. **Spec-driven development** - Start with clear requirements
2. **Incremental tasks** - Break down into small, verifiable tasks
3. **Automated execution** - Use auto-execute for routine work
4. **Quality control** - Task-checker verifies each task
5. **Flexible control** - Stop, review, and continue as needed

The key benefit: You focus on **what** to build (spec/plan/tasks), while auto-execute handles **how** to build it efficiently.
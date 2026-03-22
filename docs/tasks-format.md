# Tasks.md Format Specification

This document defines the standard format for `tasks.md` files.

## Purpose

`tasks.md` contains the actionable task checklist for implementing a feature. It's created by the `speckit` workflow (tasks phase) and consumed by the `orchestrate` loop mode.

## Format Specification

### Basic Structure

```markdown
# Tasks

## Overview
[Brief description of what these tasks accomplish]

## Task List

- [ ] T001: Task title
  Task description...

- [X] T002: Completed task title
  Task description...

## Dependencies
- T003 depends on T001
- T004 depends on T002, T003
```

### Task Format

Each task follows this structure:

```markdown
- [ ] T001: Task title (concise, actionable)

  **Description**: What needs to be done.

  **Acceptance Criteria**:
  - [ ] Criterion 1 (measurable, verifiable)
  - [ ] Criterion 2
  - [ ] Criterion 3

  **Dependencies**: T001 (if any)
  **Estimated**: 30m (optional)
  **Notes**: Additional context (optional)
```

### Task States

| Status | Checkbox | Meaning |
|--------|-----------|---------|
| Pending | `[ ]` | Task not started |
| In Progress | `[-]` | Task currently being worked on |
| Done | `[X]` | Task completed and verified |
| Failed | `[!]` | Task failed, needs attention |

### Task ID Format

- Format: `T` + 3-digit number (e.g., T001, T002, T003)
- Sequential numbering
- Gaps are allowed (e.g., T001, T005, T010)

### Acceptance Criteria Guidelines

**Good acceptance criteria**:
- ✅ Measurable: "User can sign up with email"
- ✅ Verifiable: "Password is validated (min 8 chars)"
- ✅ Testable: "Confirmation email is sent"

**Bad acceptance criteria**:
- ❌ "Implement signup" (too vague)
- ❌ "Good UX" (not measurable)
- ❌ "Follow best practices" (not verifiable)

## Example

```markdown
# Tasks

## Overview
Implement user authentication with email/password signup and login.

## Task List

- [ ] T001: Create user model and database schema

  **Description**: Define the User struct with email, password hash, and timestamps.

  **Acceptance Criteria**:
  - [ ] User struct has Email, PasswordHash, CreatedAt, UpdatedAt fields
  - [ ] Database migration creates users table
  - [ ] Email field has unique index
  - [ ] PasswordHash field is bcrypt (cost 10)

  **Estimated**: 20m

- [ ] T002: Implement signup endpoint

  **Description**: Create POST /api/auth/signup endpoint.

  **Acceptance Criteria**:
  - [ ] Endpoint accepts {email, password} JSON body
  - [ ] Validates email format
  - [ ] Validates password (min 8 chars)
  - [ ] Hashes password with bcrypt
  - [ ] Returns 201 on success with user object
  - [ ] Returns 400 on validation error
  - [ ] Returns 409 if email already exists

  **Dependencies**: T001
  **Estimated**: 30m

- [ ] T003: Implement login endpoint

  **Description**: Create POST /api/auth/login endpoint.

  **Acceptance Criteria**:
  - [ ] Endpoint accepts {email, password} JSON body
  - [ ] Verifies password against hash
  - [ ] Returns 200 with JWT token on success
  - [ ] Returns 401 on invalid credentials
  - [ ] Token contains user ID and expires in 24h

  **Dependencies**: T001
  **Estimated**: 25m

- [ ] T004: Add authentication middleware

  **Description**: Create middleware to protect routes.

  **Acceptance Criteria**:
  - [ ] Middleware validates JWT token from Authorization header
  - [ ] Extracts user ID from token
  - [ ] Sets user context for downstream handlers
  - [ ] Returns 401 if token is invalid or missing
  - [ ] Token signature verification uses correct secret

  **Dependencies**: T003
  **Estimated**: 20m

- [ ] T005: Write integration tests

  **Description**: Add tests for signup and login flows.

  **Acceptance Criteria**:
  - [ ] Test successful signup returns 201
  - [ ] Test duplicate email returns 409
  - [ ] Test invalid password returns 400
  - [ ] Test successful login returns token
  - [ ] Test invalid credentials returns 401
  - [ ] Tests use test database (isolated from dev/prod)
  - [ ] All tests pass

  **Dependencies**: T002, T003
  **Estimated**: 30m

## Dependencies
- T002 depends on T001
- T003 depends on T001
- T004 depends on T003
- T005 depends on T002, T003
```

## Integration with Orchestrate

The orchestrate loop mode reads `tasks.md` and:

1. **Parses tasks** - Extracts pending tasks with `[ ]` checkbox
2. **Extracts ID** - Gets task ID from `T###:` pattern
3. **Executes worker** - Passes task description to worker subagent
4. **Runs task-checker** - Verifies acceptance criteria are met
5. **Updates status** - Changes checkbox based on result

## Best Practices

### Task Sizing
- **Small**: 15-30 minutes per task
- **Focused**: One atomic unit of work
- **Verifiable**: Clear acceptance criteria
- If a task is > 1 hour, break it down

### Task Ordering
1. **Foundation first** - Database, models, core types
2. **Sequential** - Follow dependency chain
3. **Independent in parallel** - Tasks without dependencies can run in parallel (future)

### Acceptance Criteria
- **Be specific**: "User can sign up" vs "Implement signup"
- **Be measurable**: "Password is 8+ chars" vs "Password is validated"
- **Be complete**: Cover all important aspects, not just happy path

## Common Mistakes

- ❌ Tasks too large (should be broken down)
- ❌ Vague acceptance criteria
- ❌ Missing dependencies
- ❌ No task IDs
- ❌ Checkbox format inconsistent
- ✅ Small, focused tasks with clear acceptance criteria
- ✅ Proper dependency tracking
- ✅ Measurable and verifiable criteria

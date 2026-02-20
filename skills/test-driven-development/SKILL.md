---
name: test-driven-development
description: Use during IMPLEMENTATION phase. Write test first, watch it fail, then write minimal code to pass. This is HOW you write code, not WHEN to start a project.
---

# Test-Driven Development (TDD)

## What This Skill Is

This is a **coding technique** - how to write code correctly. It is NOT a project workflow.

```
Project workflow:    brainstorming → speckit → (implementation)
Coding technique:                          → test-driven-development
```

## The Red-Green-Refactor Cycle

```
┌─────────────────────────────────────────────┐
│                                             │
│   RED ──► GREEN ──► REFACTOR ──► (repeat)   │
│                                             │
└─────────────────────────────────────────────┘

RED:      Write a failing test
GREEN:    Write minimal code to pass
REFACTOR: Clean up while keeping tests green
```

## The Process

### 1. RED: Write a Failing Test

Before any production code:

```go
func TestUserCanLogin(t *testing.T) {
    user := User{Email: "test@example.com"}
    token, err := user.Login("password")
    assert.NoError(t, err)
    assert.NotEmpty(t, token)
}
```

Run test. Watch it fail. If it passes, you're not testing anything.

### 2. GREEN: Write Minimal Code

Write the simplest code that makes the test pass:

```go
func (u *User) Login(password string) (string, error) {
    return "token123", nil
}
```

Don't anticipate future requirements. Don't over-engineer. Just pass the test.

### 3. REFACTOR: Clean Up

With tests green, improve the code:

```go
func (u *User) Login(password string) (string, error) {
    if !u.verifyPassword(password) {
        return "", ErrInvalidPassword
    }
    return generateToken(u.ID), nil
}
```

Run tests after each change. Stay green.

## When to Use

| Scenario | Use TDD? |
|----------|----------|
| New feature | ✅ Yes |
| Bug fix | ✅ Yes - write test that reproduces bug first |
| Refactoring | ✅ Yes - tests prove behavior unchanged |
| Spike/experiment | ❌ No - throwaway code |
| One-liner fix | ⚠️ Use judgment |

## Common Problems

| Problem | Solution |
|---------|----------|
| Don't know how to test | Write wished-for API first, then assertion |
| Test too complicated | Design too complicated - simplify interface |
| Must mock everything | Code too coupled - use dependency injection |
| Test setup huge | Extract helpers, or simplify design |

## The Hard Rules

1. **No production code without a failing test**
2. **Write minimal code to pass**
3. **Refactor only when green**
4. **Never fix bugs without a test first**

## Relation to Other Skills

- **speckit**: Uses TDD during IMPLEMENT phase
- **systematic-debugging**: Uses TDD to fix bugs (write failing test first)

---

$ARGUMENTS
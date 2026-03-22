---
name: review
description: Review code quality, run tests, and verify Worker output. Use for PR review, code verification, and quality gates.
allowed-tools: [bash, read, grep]
---

# Review Skill

Verify code quality and correctness after Worker implementation.

## Review Modes

### Mode 1: Direct Review (Main Agent)

When user calls `/skill:review`, execute **directly in current agent**:

```bash
# User: /skill:review PR #123
# Action: Review directly without subagent

1. Read the code changes
2. Run tests: go test ./... -v
3. Check quality: golangci-lint run
4. Output review results
```

**When to use**:
- User explicitly asks for review
- Manual code review needed
- Quick verification

### Mode 2: Subagent Review (Orchestration)

When orchestrate.sh calls review, use **subagent**:

```bash
# In orchestrate.sh
~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
    /tmp/review-output.txt \
    5m \
    @reviewer.md \
    "Review implementation of $task"
```

**When to use**:
- Automated workflow
- Parallel task execution
- Background review

## Review Criteria

| Category | Checks |
|----------|--------|
| **Correctness** | Logic errors, edge cases, error handling |
| **Quality** | Clean code, no TODOs, proper naming |
| **Tests** | Unit tests pass, coverage adequate |
| **Style** | Follows project conventions |

## Review Workflow

```bash
# Single review
SESSION=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
    /tmp/review-output.txt \
    10m \
    @reviewer.md \
    "Review PR #123 - focus on auth logic")

tmux_wait.sh "$(echo $SESSION | cut -d: -f1)" 600

# Parallel reviews
~/.ai/skills/worker/bin/parallel.sh \
    -n 2 \
    -p @reviewer.md \
    "Review backend changes" \
    "Review frontend changes"
```

## Review Checklist

```markdown
## Review Checklist

### Correctness
- [ ] Logic is correct
- [ ] Edge cases handled
- [ ] Error handling present

### Quality
- [ ] No TODO comments
- [ ] No placeholder code
- [ ] Proper naming

### Tests
- [ ] Unit tests exist
- [ ] Tests pass
- [ ] Edge cases covered

### Style
- [ ] Follows conventions
- [ ] Consistent formatting
```

## Output Format

```bash
# Review result
{
  "status": "approved|rejected|changes_requested",
  "issues": [
    {"type": "bug", "file": "auth.go", "line": 42, "msg": "..."},
    {"type": "style", "file": "main.go", "line": 10, "msg": "..."}
  ],
  "summary": "2 issues found",
  "reviewer": "reviewer",
  "timestamp": "2024-01-15T10:00:00Z"
}
```

## Review Types

| Type | When | Typical Duration |
|------|------|------------------|
| **Code Review** | After Worker completes | 5-10 min |
| **PR Review** | Before merge | 10-15 min |
| **Security Review** | High-risk changes | 15-30 min |
| **Performance Review** | Database/network ops | 10-15 min |

## Integration with Workflow

```
Worker ──▶ Review ──▶ (if rejected) ──▶ Worker (fix)
                │
                └──▶ (if approved) ──▶ Next Phase
```

## Common Commands

```bash
# Run all tests
go test ./... -v

# Run with coverage
go test ./... -coverprofile=coverage.out

# Lint check
golangci-lint run ./...

# Format check
gofmt -d .
```

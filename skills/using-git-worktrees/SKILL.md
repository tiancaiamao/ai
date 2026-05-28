---
name: using-git-worktrees
description: Use when starting feature work that needs isolation from current workspace or before executing implementation plans - creates isolated git worktrees with smart directory selection and safety verification
---

# Using Git Worktrees

## Overview

Git worktrees create isolated workspaces sharing the same repository, allowing work on multiple branches simultaneously without switching.

**Core principle:** Systematic directory selection + safety verification = reliable isolation.

**Announce at start:** "I'm using the using-git-worktrees skill to set up an isolated workspace."

---

## ⛔ MANDATORY — Worktree Creation Checklist

**创建 worktree 后、开始任何工作之前，必须完成以下全部项目：**

- [ ] **目录选择** — 按优先级：existing `.worktrees/` > CLAUDE.md > ask user
- [ ] **验证 gitignore** — `git check-ignore` 确认目录被忽略（project-local 时）
- [ ] **创建 worktree** — `git worktree add <path> -b <branch>`
- [ ] **切换工作区** — 使用 `change_workspace` 工具，不是 `cd`
- [ ] **安装依赖** — `go mod download` / `npm install` 等
- [ ] **验证基线** — `go test ./...` 或等效命令，确保测试全部通过
- [ ] **报告位置** — 向用户报告路径、测试结果、准备就绪

**只有全部通过才开始 feature 工作。** 基线测试失败时必须报告并询问是否继续。

## Directory Selection Process

Follow this priority order:

### 1. Check Existing Directories

```bash
# Check in priority order
ls -d .worktrees 2>/dev/null     # Preferred (hidden)
ls -d worktrees 2>/dev/null      # Alternative
```

**If found:** Use that directory. If both exist, `.worktrees` wins.

### 2. Check CLAUDE.md

```bash
grep -i "worktree.*director" CLAUDE.md 2>/dev/null
```

**If preference specified:** Use it without asking.

### 3. Ask User

If no directory exists and no CLAUDE.md preference:

```
No worktree directory found. Where should I create worktrees?

1. .worktrees/ (project-local, hidden)
2. ~/.config/superpowers/worktrees/<project-name>/ (global location)

Which would you prefer?
```

## Safety Verification

### For Project-Local Directories (.worktrees or worktrees)

**MUST verify directory is ignored before creating worktree:**

```bash
# Check if directory is ignored (respects local, global, and system gitignore)
git check-ignore -q .worktrees 2>/dev/null || git check-ignore -q worktrees 2>/dev/null
```

**If NOT ignored:**

Per Jesse's rule "Fix broken things immediately":
1. Add appropriate line to .gitignore
2. Commit the change
3. Proceed with worktree creation

**Why critical:** Prevents accidentally committing worktree contents to repository.

### For Global Directory (~/.config/superpowers/worktrees)

No .gitignore verification needed - outside project entirely.

## Creation Steps

### 1. Detect Project Name

```bash
project=$(basename "$(git rev-parse --show-toplevel)")
```

### 2. Create Worktree

```bash
# Determine full path
case $LOCATION in
  .worktrees|worktrees)
    path="$LOCATION/$BRANCH_NAME"
    ;;
  ~/.config/superpowers/worktrees/*)
    path="~/.config/superpowers/worktrees/$project/$BRANCH_NAME"
    ;;
esac

# Create worktree with new branch
git worktree add "$path" -b "$BRANCH_NAME"

# IMPORTANT: After creating the worktree, use the change_workspace tool
# This switches the agent's workspace to the new worktree directory.
# All subsequent file operations (read, write, grep, edit, bash) will be relative to the new workspace.
#
# Example tool call:
# <change_workspace path="$path"/>
#
# DO NOT use 'cd' in bash - it only affects the bash subprocess, not the agent's workspace.

### 3. Run Project Setup

Auto-detect and run appropriate setup:

```bash
# Node.js
if [ -f package.json ]; then npm install; fi

# Rust
if [ -f Cargo.toml ]; then cargo build; fi

# Python
if [ -f requirements.txt ]; then pip install -r requirements.txt; fi
if [ -f pyproject.toml ]; then poetry install; fi

# Go
if [ -f go.mod ]; then go mod download; fi
```

### 4. Verify Clean Baseline

Run tests to ensure worktree starts clean:

```bash
# Examples - use project-appropriate command
npm test
cargo test
pytest
go test ./...
```

**If tests fail:** Report failures, ask whether to proceed or investigate.

**If tests pass:** Report ready.

### 5. Report Location

```
Worktree ready at <full-path>
Tests passing (<N> tests, 0 failures)
Ready to implement <feature-name>
```

## Quick Reference

| Situation | Action |
|-----------|--------|
| `.worktrees/` exists | Use it (verify ignored) |
| `worktrees/` exists | Use it (verify ignored) |
| Both exist | Use `.worktrees/` |
| Neither exists | Check CLAUDE.md → Ask user |
| Directory not ignored | Add to .gitignore + commit |
| Tests fail during baseline | Report failures + ask |
| No package.json/Cargo.toml | Skip dependency install |

## Common Mistakes

### Skipping ignore verification

- **Problem:** Worktree contents get tracked, pollute git status
- **Fix:** Always use `git check-ignore` before creating project-local worktree

### Assuming directory location

- **Problem:** Creates inconsistency, violates project conventions
- **Fix:** Follow priority: existing > CLAUDE.md > ask

### Proceeding with failing tests

- **Problem:** Can't distinguish new bugs from pre-existing issues
- **Fix:** Report failures, get explicit permission to proceed

### Hardcoding setup commands

- **Problem:** Breaks on projects using different tools
- **Fix:** Auto-detect from project files (package.json, etc.)

## Example Workflow

```
You: I'm using the using-git-worktrees skill to set up an isolated workspace.

[Check .worktrees/ - exists]
[Verify ignored - git check-ignore confirms .worktrees/ is ignored]
[Create worktree: git worktree add .worktrees/auth -b feature/auth]
[Use change_workspace tool: change_workspace path=".worktrees/auth"]
[Run npm install in the new workspace]
[Run npm test - 47 passing]

Worktree ready at /Users/jesse/myproject/.worktrees/auth
Tests passing (47 tests, 0 failures)
Ready to implement auth feature
```

## Red Flags

**Never:**
- Create worktree without verifying it's ignored (project-local)
- Skip baseline test verification
- Proceed with failing tests without asking
- Assume directory location when ambiguous
- Skip CLAUDE.md check

**Always:**
- Follow directory priority: existing > CLAUDE.md > ask
- Verify directory is ignored for project-local
- Auto-detect and run project setup
- Verify clean test baseline

## Integration

**Called by:**
- **brainstorming** (Phase 4) - REQUIRED when design is approved and implementation follows
- **subagent-driven-development** - REQUIRED before executing any tasks
- **executing-plans** - REQUIRED before executing any tasks
- Any skill needing isolated workspace

**Pairs with:**
- **finishing-a-development-branch** - REQUIRED for cleanup after work complete
- **subagent** - Combine for persistent, observable task execution with ai serve

## Agent Worktrees

Combine git worktrees with `subagent` 技能 for persistent, observable task execution in isolated environments.

### When to Use

| Scenario | Approach | Why |
|----------|----------|-----|
| Long-running analysis | Worktree + subagent | Results persist in worktree after completion |
| Parallel feature work | Multiple worktrees | Isolated branches, no conflicts |
| Code review before merge | Worktree + review agent | Read-only review in isolated environment |
| Experimental changes | Worktree + coder agent | Safely iterate without affecting main branch |

### Workflow

```bash
# 1. Create worktree for the task
git worktree add .worktrees/review-auth -b review/auth
```

2. 用 `subagent` 技能 spawn 子 agent，参数：

| 参数 | 值 |
|------|-----|
| system-prompt | `@/path/to/reviewer.md` |
| input | `'Review auth changes for security issues'` |
| name | `review-auth` |
| timeout | `15m` |

3. 用 `subagent` 技能 Watch 等待完成
4. 用 `subagent` 技能 Cleanup 清理 agent
5. 结果保留在 worktree 中供检查

### Pattern: Persistent Review Session

```bash
git worktree add .worktrees/review-feature-x -b review/feature-x
```

用 `subagent` 技能 spawn + watch，参数：

| 参数 | 值 |
|------|-----|
| system-prompt | `@/path/to/reviewer.md` |
| input | `'Review changes against main: security, correctness, style'` |
| name | `review-feature-x` |
| timeout | `15m` |

完成后 Cleanup。Worktree 保留，可用于手动检查、追加 review、测试。

### Pattern: Parallel Feature Branches

```bash
git worktree add .worktrees/feature-auth -b feature/auth
git worktree add .worktrees/feature-api -b feature/api
```

用 `subagent` 技能并行 spawn 2 个 agent（注意并发上限），参数：

| Agent | system-prompt | input | name | timeout |
|-------|---------------|-------|------|---------|
| Auth | `@/path/to/builder.md` | `'Implement auth feature in current worktree'` | `build-auth` | `15m` |
| API | `@/path/to/builder.md` | `'Implement API feature in current worktree'` | `build-api` | `15m` |

> 完整 spawn/watch/cleanup 代码见 `subagent` 技能。每个 worktree 中的改动互不干扰，可独立 merge。

### Benefits Over Temporary Agents

| Temporary Agent | Worktree Agent |
|----------------|----------------|
| Results lost after completion | Results persist in worktree |
| Hard to inspect intermediate state | Full git history available |
| Difficult to debug failures | Can check worktree state and git diff |
| Single-shot execution | Can spawn new agents and iterate |

### Cleanup

After task completion, use the `finishing-a-development-branch` skill to clean up worktrees.

**See also:** `subagent` skill for ai serve/send/watch/kill pattern details

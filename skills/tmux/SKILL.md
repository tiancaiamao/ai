---
name: tmux
description: Use tmux for background tasks, TUI testing, and debugging layouts. Philosophy: No background bash - use tmux for full observability and direct interaction.
---

# tmux - No Background Bash Philosophy

## Core Philosophy

> **No background bash.** Use tmux. Full observability, direct interaction.

Instead of implementing complex background task management in your application:

| ❌ 应用内实现 | ✅ 用 tmux |
|-------------|----------|
| `&` 后台运行，丢失输出 | tmux session 保持运行，随时查看 |
| 需要实现 job control | tmux 已有完整的 session 管理 |
| 输出不可见 | `tmux attach` 随时重连 |
| 进程难以管理 | `tmux ls` / `tmux kill-session` |

**tmux gives you:**
- Full observability - always see what's happening
- Direct interaction - attach and type commands
- Persistence - detach and reattach later
- Isolation - each task in its own session

## ⛔ Safety Rules — READ FIRST

> **Agent 误操作 `tmux kill-server` 曾导致用户丢失全部 tmux session。**
> 以下规则是硬约束，违反即可能造成不可恢复的数据丢失。

### 绝对禁止

| 禁令 | 原因 |
|------|------|
| **禁止 `tmux kill-server`** | 销毁整个 tmux 服务器，所有用户 session 全部消失 |
| **禁止遍历所有 session 并批量 kill** | 通配符可能命中用户的 session，最后一个 session 被杀后 server 也会退出 |
| **禁止 kill 不是你创建的 session** | 你不知道其他 session 的用途 |
| **禁止向唯一 pane 发送 `exit` / `C-d`** | pane 关闭 → window 关闭 → session 关闭，连锁退出 |

### 正确做法

```bash
# ✅ 只 kill 你自己创建的、有明确名称的 session
tmux kill-session -t "my-agent-task" 2>/dev/null

# ✅ 需要隔离时，用 -L 创建独立 tmux 服务器（不影响默认服务器）
tmux -L agent-$$ new-session -d -s "my-task" "..."
tmux -L agent-$$ kill-session -t "my-task"

# ✅ 清理前先检查 session 是否是你创建的
tmux list-sessions  # 确认目标名称再操作
```

### 如果需要"干净环境"

```bash
# ❌ 错误：kill-server 毁掉一切
tmux kill-server

# ✅ 正确：只清理你自己的 session，用明确的命名前缀
for s in $(tmux list-sessions -F '#{session_name}' 2>/dev/null | grep '^my-prefix-'); do
  tmux kill-session -t "$s" 2>/dev/null
done

# ✅ 更好：用隔离 socket，完全不影响用户
TMUX_SOCK="tmux -L agent-$$"
$TMUX_SOCK new-session -d -s "task" "..."
# 清理时 kill-session 也不会影响用户
$TMUX_SOCK kill-session -t "task"
```

## Quick Reference

### Session Management

```bash
tmux new -s <name>              # Create named session
tmux new -s build -d            # Create detached (background)
tmux attach -t <name>           # Attach to session
tmux detach (Ctrl-b d)          # Detach from session
tmux ls                         # List sessions
tmux kill-session -t <name>     # Kill specific session
# ⛔ NEVER: tmux kill-server     # 会销毁所有 session，绝对禁止
```

### Window & Pane Operations

```bash
# Panes
Ctrl-b %                        # Split vertical (left/right)
Ctrl-b "                        # Split horizontal (top/bottom)
Ctrl-b <arrow>                  # Navigate panes
Ctrl-b x                        # Kill pane
Ctrl-b z                        # Toggle pane zoom (fullscreen)
Ctrl-b Ctrl-<arrow>             # Resize pane
Ctrl-b { / }                    # Swap panes

# Windows
Ctrl-b c                        # New window
Ctrl-b n / p                    # Next / Previous window
Ctrl-b <number>                 # Go to window number
Ctrl-b ,                        # Rename window
Ctrl-b &                        # Kill window
```

### Sending Commands (for scripting)

```bash
tmux send-keys -t <session> "command" Enter
tmux send-keys -t <session> Escape           # Escape key
tmux send-keys -t <session> C-c              # Ctrl+C
tmux send-keys -t <session> C-o              # Ctrl+O
tmux send-keys -t <session> M-x              # Alt+X
tmux send-keys -t <session> Space            # Space
tmux send-keys -t <session> Up               # Arrow up
tmux capture-pane -t <session> -p            # Capture screen output
tmux capture-pane -t <session> -p > out.txt  # Save to file
```

## Common Workflows

### Long-Running Build / Test

```bash
# Start build in tmux (can detach and go do other things)
tmux new -s build
go build ./...  # or make, npm build, etc.

# Detach: Ctrl-b d
# Go do other work...

# Reattach later to check progress
tmux attach -t build
```

### Debugging Layout (Go + Delve)

```
┌─────────────────────────────────────────────────────┐
│  Pane 0: Program                                    │
│  $ go run ./cmd/ai                                  │
└─────────────────────────────────────────────────────┘
┌─────────────────────────────────────────────────────┐
│  Pane 1: Delve Debugger                             │
│  (dlv) break main.main                              │
│  (dlv) continue                                     │
└─────────────────────────────────────────────────────┘
```

```bash
# Create debug session
tmux new -s debug -d
tmux split-window -v -t debug

# Top: run program
tmux send-keys -t debug:0.0 "go run ./cmd/ai" Enter

# Bottom: attach debugger
tmux send-keys -t debug:0.1 "dlv attach \$(pgrep ai)" Enter

tmux attach -t debug
```

### TDD Layout (Code + Test Watch)

```
┌──────────────────────────────┬──────────────────────────────┐
│  Pane 0: Program             │  Pane 1: Test Watch          │
│  $ go run ./cmd/ai           │  $ go test -v ./...          │
├──────────────────────────────┴──────────────────────────────┤
│  Pane 2: Logs / Shell                                       │
│  $ tail -f /var/log/ai.log                                  │
└─────────────────────────────────────────────────────────────┘
```

```bash
# Create TDD session
tmux new -s tdd -d
tmux split-window -h -t tdd
tmux split-window -v -t tdd:0.0

# Top-left: run program
tmux send-keys -t tdd:0.0 "go run ./cmd/ai" Enter

# Top-right: watch tests
tmux send-keys -t tdd:0.1 "go test -v ./..." Enter

# Bottom: logs
tmux send-keys -t tdd:0.2 "tail -f /var/log/ai.log" Enter

tmux attach -t tdd
```

### Multiple Worktrees in Parallel

```bash
# Each worktree gets its own window
tmux new -s worktrees -d -c $(git rev-parse --show-toplevel)
tmux rename-window -t worktrees:0 "main"

# Add windows for each worktree
for wt in $(git worktree list --porcelain | grep "^worktree" | cut -d' ' -f2); do
    name=$(basename "$wt")
    tmux new-window -t worktrees -n "$name" -c "$wt"
done

tmux attach -t worktrees
```

## TUI Testing Pattern

Automated testing of terminal applications with controlled environment:

```bash
#!/bin/bash
# test-tui.sh - Automated TUI testing

SESSION="tui-test"
TMUX="tmux -L test"  # Use separate socket for isolation

cleanup() {
    $TMUX kill-session -t $SESSION 2>/dev/null
}
trap cleanup EXIT

# Create session with specific dimensions (deterministic testing)
$TMUX new-session -d -s $SESSION -x 120 -y 40

# Start the TUI application
$TMUX send-keys -t $SESSION "go run ./cmd/ai" Enter
sleep 2  # Wait for startup

# Capture initial state
$TMUX capture-pane -t $SESSION -p > /tmp/initial-state.txt

# Send user input
$TMUX send-keys -t $SESSION "help" Enter
sleep 1

# Capture after input
$TMUX capture-pane -t $SESSION -p > /tmp/after-help.txt

# Verify output
if grep -q "Available commands" /tmp/after-help.txt; then
    echo "✅ Test passed: Help command works"
else
    echo "❌ Test failed: Help command not working"
    cat /tmp/after-help.txt
    exit 1
fi

# Send special keys
$TMUX send-keys -t $SESSION Escape
$TMUX send-keys -t $SESSION C-c

echo "✅ All tests passed"
```

## Special Keys Reference

| Key | tmux send-keys syntax |
|-----|----------------------|
| Escape | `Escape` |
| Enter | `Enter` |
| Space | `Space` |
| Tab | `Tab` |
| Backspace | `BSpace` |
| Delete | `DC` |
| Insert | `IC` |
| Home | `Home` |
| End | `End` |
| Page Up | `PPrior` |
| Page Down | `PPage` |
| Arrow keys | `Up` `Down` `Left` `Right` |
| Ctrl+X | `C-x` (lowercase) |
| Alt+X | `M-x` (lowercase) |
| Shift+Tab | `BTab` |
| F1-F12 | `F1` `F2` ... `F12` |

## Configuration

Add to `~/.tmux.conf`:

```bash
# Enable mouse (scroll, click to select pane)
set -g mouse on

# Start window numbering at 1 (easier to reach)
set -g base-index 1
setw -g pane-base-index 1

# Increase scrollback buffer
set -g history-limit 50000

# Better colors
set -g default-terminal "screen-256color"

# Easier split keys (| and -)
bind | split-window -h -c "#{pane_current_path}"
bind - split-window -v -c "#{pane_current_path}"

# Quick reload config
bind r source-file ~/.tmux.conf \; display "Config reloaded!"

# Status bar with git branch
set -g status-right '#(git branch 2>/dev/null | grep -e "\*" | sed "s/* //") | %H:%M'

# Faster key repeat
set -s escape-time 0

# Don't rename windows automatically
set -g allow-rename off
```

## Integration with ad Editor

When running ai as a plugin in ad editor:

```bash
# Dedicated session for ad + ai development
tmux new -s ad-dev -d

# Split: ad editor | ai REPL (if needed)
tmux split-window -h -t ad-dev

# Left: ad editor with ai plugin
tmux send-keys -t ad-dev:0.0 "ad ." Enter

# Right: shell for testing
tmux send-keys -t ad-dev:0.1 "# shell for go test, git, etc." Enter

tmux attach -t ad-dev
```

## Checking Background Task Status

When you've started a long-running task in tmux, use these commands to check status:

| Task | Command |
|------|---------|
| List all sessions | `tmux ls` |
| Check if session exists | `tmux ls \| grep <name>` |
| View current output | `tmux capture-pane -t <name> -p` |
| View last N lines | `tmux capture-pane -t <name> -p -S -50` |
| Attach to session | `tmux attach -t <name>` |
| Send interrupt (Ctrl+C) | `tmux send-keys -t <name> C-c` |
| Kill session | `tmux kill-session -t <name>` ⛔ **禁止 `kill-server`** |
| Wait for completion | `tmux_wait.sh <name> [timeout]` |

**Example workflow:**
```bash
# 1. Start long build
tmux new -s build -d "go build ./... 2>&1 | tee /tmp/build.log"

# 2. Do other work...

# 3. Check progress
tmux capture-pane -t build -p -S -20

# 4. Or attach to see full output
tmux attach -t build

# 5. Wait for completion in script
~/.ai/skills/tmux/bin/tmux_wait.sh build 600
```

## When to Use

| Scenario | tmux Pattern |
|----------|-------------|
| Long build/test | Single session, detach |
| Debugging | Split pane: program + debugger |
| TDD workflow | Split pane: program + test watch |
| Multiple features | Window per worktree |
| TUI testing | Automated script with capture-pane |

## Summary

**Don't implement background task management in your app. Use tmux.**

- ✅ Simple - no code to write
- ✅ Observable - always see output
- ✅ Interactive - attach anytime
- ✅ Persistent - survives disconnect
- ✅ Standard - everyone knows tmux
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

## Quick Reference

### Session Management

```bash
tmux new -s <name>              # Create named session
tmux new -s build -d            # Create detached (background)
tmux attach -t <name>           # Attach to session
tmux detach (Ctrl-b d)          # Detach from session
tmux ls                         # List sessions
tmux kill-session -t <name>     # Kill session
tmux kill-server                # Kill all sessions
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
# ADR 003: Tmux for Subagent Isolation

**Status:** Accepted
**Date:** 2024-03-15
**Context:** Subagent implementation for multi-agent workflows

## Context

When implementing subagent isolation for focused task execution, we needed to choose between:

1. **Goroutines** (in-process isolation)
2. **Docker containers** (container isolation)
3. **Tmux sessions** (terminal session isolation)
4. **Separate processes** (no terminal attachment)
5. **Namespaces/cgroups** (OS-level isolation)

## Decision

Chose Tmux sessions for subagent isolation.

## Rationale

### Why Tmux?

1. **Debuggability**
   - Easy to inspect subagent behavior: `tmux attach -t <session>`
   - Full terminal output captured (including colors)
   - Can monitor long-running tasks in real-time
   - Can kill entire session tree easily

2. **Simplicity**
   - Lightweight (no container overhead)
   - Fast startup (~2-3 seconds)
   - Easy to use (single command to start)
   - Automatic cleanup on completion

3. **Cross-Platform Support**
   - Works on Linux, macOS, BSD
   - Widely available (package managers)
   - No special kernel features required

4. **Output Capture**
   - Captures all stdout/stderr
   - Preserves terminal colors and formatting
   - Can redirect to file easily
   - Can use tmux `capture-pane` for snapshots

### Usage Pattern

```bash
# Start subagent with focused prompt
SESSION=$(start_subagent @reviewer.md "Review PR #42")

# Wait for completion and capture output
OUTPUT_FILE=/tmp/review-output.txt
tmux_wait.sh $SESSION $OUTPUT_FILE 600

# Read the structured result
cat $OUTPUT_FILE
```

### Disadvantages

1. **Dependency**
   - Requires tmux installation
   - Not installed by default on all systems

2. **Platform Limitations**
   - Limited Windows support (WSL ok, native problematic)
   - Requires proper terminal support

3. **Startup Overhead**
   - ~2-3 seconds to create tmux session
   - Non-trivial for many subagents

4. **Session Management**
   - Need to clean up sessions on failure
   - Potential for session leaks

### Rejected Alternatives

#### Goroutines (In-Process)

**Rejected because:**
- No isolation (shared memory, same process)
- Hard to debug (can't inspect independently)
- Output mixed with main agent
- No resource isolation

#### Docker Containers

**Rejected because:**
- Too heavy for lightweight tasks
- Longer startup time
- Requires Docker daemon
- Platform-specific
- Overkill for just output capture

#### Separate Processes (No Terminal)

**Rejected because:**
- Hard to debug (can't attach)
- Can't monitor in real-time
- No terminal output capture
- Hard to control process lifecycle

#### Namespaces/Cgroups

**Rejected because:**
- Complex to implement
- Requires root privileges
- Platform-specific (Linux only)
- Overkill for this use case

## Consequences

### Positive

- Excellent debugging experience (can attach to any session)
- Full output capture (including colors)
- Easy to monitor long-running tasks
- Automatic cleanup on completion
- Can kill entire session tree
- Works across platforms (Linux/macOS)

### Negative

- Requires tmux dependency
- Slight startup overhead (2-3s)
- Limited Windows support
- Need to manage session cleanup

### Mitigations

- Add graceful fallback for Windows (separate processes)
- Provide tmux installation instructions
- Implement automatic session cleanup
- Document troubleshooting steps
- Add health checks for stale sessions

## Implementation

### Key Functions

1. **`start_subagent`** - Create tmux session
   - Spawn tmux session with focused prompt
   - Return session ID for tracking

2. **`tmux_wait.sh`** - Wait for completion
   - Poll for session completion
   - Capture output to file
   - Timeout handling

3. **Cleanup**
   - Kill session on timeout
   - Kill session on completion
   - Cleanup stale sessions

### Session Naming

```
subagent_<timestamp>_<short_hash>
```

### Output Capture

```bash
# Capture pane output
tmux capture-pane -t $SESSION -S - -E - > $OUTPUT_FILE
```

### Health Checks

```bash
# Check for stale sessions (> 1 hour old)
tmux list-sessions -F "#{session_name} #{session_created_string}" |
  awk '$2 < "-1 hour ago" {print $1}' | xargs tmux kill-session -t
```

## Usage Examples

### Code Review Subagent

```bash
# Start reviewer agent
SESSION=$(start_subagent @reviewer.md "Review PR #42. Output to /tmp/review.json")

# Wait for completion (10 min timeout)
tmux_wait.sh $SESSION /tmp/review.json 600

# Read result
cat /tmp/review.json
```

### Debug Subagent

```bash
# Attach to running subagent for debugging
tmux attach -t $SESSION

# Detach when done: Ctrl+b, d
```

### Parallel Subagents

```bash
# Start multiple subagents in parallel
SESSION1=$(start_subagent @reviewer.md "Review PR #1")
SESSION2=$(start_subagent @debugger.md "Debug test failure")
SESSION3=$(start_subagent @tester.md "Run integration tests")

# Wait for all
wait_all_sessions $SESSION1 $SESSION2 $SESSION3
```

## Troubleshooting

### Session Leaks

```bash
# List all subagent sessions
tmux list-sessions | grep subagent_

# Kill stale sessions
tmux kill-session -t subagent_<stale_id>
```

### Startup Failures

```bash
# Check if tmux is installed
which tmux

# Check tmux version
tmux -V

# Try creating a test session
tmux new-session -d -s test sleep 1
tmux kill-session -t test
```

### Output Not Captured

```bash
# Check output file permissions
ls -la /tmp/output.txt

# Check session status
tmux list-sessions

# Manual capture
tmux capture-pane -t $SESSION -S - -E - > /tmp/output.txt
```

## Future Considerations

### Potential Improvements

1. **Session Pool** - Reuse sessions for faster startup
2. **Better Windows Support** - Use WSL or separate processes
3. **Metrics** - Track session lifecycle metrics
4. **Session Limits** - Limit concurrent sessions

### Out of Scope

- Container-based isolation (too heavy)
- Remote subagents (local-only for now)
- Subagent networking (isolated sessions)

## References

- Related: ADR 001 (RPC-First Design)
- Related: ADR 002 (Code-Driven Workflow)
- Subagent implementation: `skills/subagent/`
- Tmux documentation: https://github.com/tmux/tmux/wiki
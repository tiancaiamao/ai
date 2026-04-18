# Design: ag CLI Redesign

## Problem

`ag` is the orchestration infrastructure for LLM agents. Current shortcomings:

1. **No runtime intervention** — After `ag spawn`, agent is a black box. Leader can only `wait` or `kill`. Cannot steer direction, abort task, or reuse session.
2. **No structured status** — `ag status` returns a single word. LLM cannot determine what agent is doing, whether it's stuck, or how much resources it consumed.
3. **No structured task results** — `ag output` is raw text. LLM must interpret success/failure from free-form output.

All three depend on one prerequisite: **ag needs bidirectional communication with running agents**.

## Key Insight from pi-agent-teams

`ai --mode rpc` already provides full bidirectional JSON-RPC:
- `prompt` / `steer` / `follow_up` / `abort` commands
- Event stream: `agent_start/end`, `turn_start/end`, `tool_execution_start/end`, `message_update` (text_delta)
- `get_state` / `get_session_stats`

The problem isn't that `ai` lacks capability — it's that `ag` doesn't use it. Current `ag spawn` uses headless mode (tmux black box).

## Critical Design Constraint

**`ag` is a CLI tool, not a long-running process.** Unlike pi-agent-teams where the leader is a VSCode extension that lives for the entire session, `ag` commands are invoked by the leader agent and exit immediately. This means:

- `ag agent spawn` spawns an agent and returns — the CLI process exits
- The agent (`ai --mode rpc`) needs something to hold its stdin/stdout pipes after `ag` exits
- Without a pipe holder, there's no way to send steer/abort/prompt later

### Why Not Direct Process Holding (pi-agent-teams style)?

pi-agent-teams uses `child_process.spawn("pi", ["--mode", "rpc"])` directly because the leader extension is long-lived — it holds the pipes for the entire session. This doesn't work for `ag` because `ag` exits after each command.

### Why Not a Central Daemon?

A single daemon process holding all agent pipes introduces a single point of failure — if the daemon crashes, all agents die. It also requires daemon lifecycle management (auto-start, health check, restart).

## Architecture: Bridge-per-Agent in Tmux

Each agent gets its own bridge process running inside a tmux session. The bridge holds the `ai --mode rpc` stdin/stdout pipes and exposes a Unix socket for external control.

```
ag agent spawn worker-1 --input "fix bugs"
  │
  └── tmux new-session -d -s ag-worker-1 -- "ag bridge worker-1"
      │
      └── [ag bridge process, running inside tmux]
          ├── exec.Command("ai", "--mode", "rpc")
          │   ├── cmd.StdinPipe()  → send prompt/steer/abort
          │   └── cmd.StdoutPipe() → read event stream → activity.json
          ├── Send initial prompt
          ├── Event reader goroutine → writes activity.json
          └── Listen on .ag/agents/worker-1/bridge.sock (Unix socket)

ag agent steer worker-1 "不要用 lib X"
  ├── dial .ag/agents/worker-1/bridge.sock
  ├── send {"type":"steer","message":"不要用 lib X"}
  ├── read response
  └── close connection (CLI exits)

ag agent status worker-1
  └── read .ag/agents/worker-1/activity.json (no socket needed)

ag agent kill worker-1
  └── tmux kill-session -t ag-worker-1
```

### Why This Works

| Concern | Solution |
|---------|----------|
| ag CLI exits immediately | Bridge runs in tmux, independent of ag CLI lifecycle |
| Need to send steer/abort later | Bridge exposes Unix socket, CLI connects on demand |
| Agent observability | `tmux attach -t ag-worker-1` to see live output |
| No single point of failure | Each agent has its own bridge, crashes are isolated |
| No daemon lifecycle management | tmux handles process persistence |
| Bridge code reuse | `ag bridge <id>` is an internal subcommand of the same binary |

### Comparison with Alternatives

| Dimension | Central Daemon | Bridge-per-agent (chosen) | Direct spawn (pi-agent-teams) |
|-----------|---------------|---------------------------|-------------------------------|
| Pipe holder | 1 daemon | N bridges | Leader extension |
| Single point of failure | Yes — daemon dies = all die | No — isolated | Leader dies = all die |
| Lifecycle mgmt | Auto-start, health check | tmux handles it | N/A (same process) |
| Observability | daemon.log | tmux attach per agent | N/A |
| Complexity | Socket multiplexing | Simple 1:1 bridge:agent | Simplest |
| Fits CLI model | Yes | Yes | No — requires long-lived spawner |

### Bridge Process Internals

`ag bridge <id>` is an internal subcommand that:

1. Creates `bridge.sock` listener **before** starting `ai` (ensures socket exists when spawn returns)
2. Reads `.ag/agents/<id>/meta.json` for spawn config (system prompt, timeout, cwd)
3. Starts `ai --mode rpc` as a child process with piped stdin/stdout
4. Redirects own stderr to `.ag/agents/<id>/bridge-stderr`
5. Sends the initial prompt via stdin pipe
6. Starts event reader goroutine that writes `activity.json` on significant events
7. Accepts connections on `bridge.sock`, forwards commands to `ai`, returns responses
8. On agent exit: updates status, writes final `activity.json`, closes socket, exits

```go
// Simplified bridge main loop
func runBridge(agentID string) error {
    agentDir := filepath.Join(".ag", "agents", agentID)
    
    // 1. Create socket BEFORE starting ai
    os.Remove(filepath.Join(agentDir, "bridge.sock")) // clean stale
    listener, err := net.Listen("unix", filepath.Join(agentDir, "bridge.sock"))
    if err != nil { return err }
    defer listener.Close()
    
    // 2. Load spawn config
    meta := loadMeta(agentDir)
    
    // 3. Start ai --mode rpc
    cmd := exec.Command("ai", "--mode", "rpc", "--timeout", meta.Timeout, ...)
    stdin, _ := cmd.StdinPipe()
    stdout, _ := cmd.StdoutPipe()
    stderrFile, _ := os.Create(filepath.Join(agentDir, "stderr"))
    cmd.Stderr = stderrFile
    cmd.Start()
    
    // Record ai PID in activity.json (not bridge PID)
    writeActivity(agentDir, Activity{Status: "running", PID: cmd.Process.Pid, ...})
    
    // 4. Send initial prompt
    sendRPC(stdin, "prompt", meta.Input)
    
    // 5. Event reader goroutine
    go func() {
        scanner := bufio.NewScanner(stdout)
        var outputBuf bytes.Buffer // accumulates full output
        for scanner.Scan() {
            event := parseEvent(scanner.Bytes())
            updateActivity(agentDir, event)
            // Accumulate text deltas into output
            if event.Type == "message_update" && event.TextDelta != "" {
                outputBuf.WriteString(event.TextDelta)
            }
        }
        // stdout EOF = agent exited
        exitErr := cmd.Wait()
        activity := finalizeActivity(exitErr, outputBuf.String())
        writeActivityAtomic(agentDir, activity)
        os.WriteFile(filepath.Join(agentDir, "output"), outputBuf.Bytes(), 0644)
        listener.Close() // break accept loop
    }()
    
    // 6. Accept connections
    for {
        conn, err := listener.Accept()
        if err != nil { break } // listener closed on agent exit
        go handleBridgeConn(conn, stdin, agentDir)
    }
    return nil
}

func handleBridgeConn(conn net.Conn, stdin io.WriteCloser, agentDir string) {
    defer conn.Close()
    // Read one request, process, respond, close (HTTP-style, one request per connection)
    req := readJSONLine(conn)
    
    switch req.Type {
    case "steer", "abort", "prompt", "shutdown_request":
        resp := forwardToAI(stdin, req)
        writeJSONLine(conn, resp)
    case "get_state":
        // Read from disk, don't bother ai
        activity := readActivity(agentDir)
        writeJSONLine(conn, BridgeResponse{OK: true, Data: activity})
    default:
        writeJSONLine(conn, BridgeResponse{OK: false, Error: "unknown command: " + req.Type})
    }
}
```

### Bridge Socket Protocol

**Framing**: Newline-delimited JSON. One JSON object per line. Matches `ai --mode rpc`'s own protocol.

**Connection model**: One connection = one request + one response. HTTP-style. Client connects, sends request, reads response, closes. This avoids connection state management and supports concurrent CLI invocations naturally.

**Request schema** (CLI → bridge):

```jsonc
{
  "type": "steer | abort | prompt | shutdown_request | get_state",
  "message": "...",              // required for steer, prompt
  "requestId": "...",            // optional, for shutdown_request correlation
  "file": "..."                  // optional, file path to include with prompt
}
```

**Response schema** (bridge → CLI):

```jsonc
{
  "ok": true,
  "data": { ... },               // for get_state: AgentActivity struct
  "error": "..."                  // present only when ok=false
}
```

**Command-specific details**:

| Command | Request fields | Success response | Failure cases |
|---------|---------------|-----------------|---------------|
| `steer` | `message` (required) | `{"ok": true}` | agent not running, ai timeout |
| `abort` | — | `{"ok": true}` | agent not running, ai timeout |
| `prompt` | `message` (required), `file` (optional) | `{"ok": true}` | agent not running, ai timeout |
| `shutdown_request` | `requestId` (required) | `{"ok": true}` or `{"ok": false, "error": "agent busy"}` | agent not running, timeout (30s) |
| `get_state` | — | `{"ok": true, "data": {...activity.json...}}` | — |

**Error codes** (for `ok: false`):

| Error | Meaning |
|-------|---------|
| `"agent not running"` | ai process exited, bridge is in cleanup state |
| `"ai timeout"` | ai did not respond within the command timeout |
| `"agent busy"` | shutdown rejected because agent has active task |
| `"unknown command"` | unrecognized `type` field |

### Agent Startup Sequence

```
ag agent spawn worker-1 --system @impl.md --input "实现登录" --timeout 10m
  │
  ├── 1. Validate agent ID (alphanumeric + hyphens + underscores, max 64 chars)
  ├── 2. Validate tmux is available (which tmux)
  ├── 3. Create .ag/agents/worker-1/ directory
  ├── 4. Write meta.json (id, system_prompt, input, timeout, cwd, spawnedAt)
  ├── 5. Write initial activity.json (status: "spawning")
  ├── 6. tmux new-session -d -s ag-worker-1 -- "ag bridge worker-1"
  ├── 7. Poll bridge.sock until it appears (max 10s, check every 200ms)
  ├── 8. If socket appeared → read activity.json, confirm status is "running"
  │      If not → error: bridge failed to start (check .ag/agents/worker-1/bridge-stderr)
  └── 9. Print spawn result, exit
```

`ag agent spawn` **blocks until bridge.sock is ready** (step 7). This ensures callers can immediately run `ag agent steer` after spawn returns. If the socket never appears within 10s, spawn fails with error.

### Event Stream Persistence

The bridge's event reader goroutine writes structured activity data:

```go
type AgentActivity struct {
    Status         string    `json:"status"`                    // spawning, running, idle, done, failed, killed
    PID            int       `json:"pid"`                       // ai process PID (not bridge PID)
    StartTime      time.Time `json:"start_time"`
    Turns          int       `json:"turns"`
    Tokens         int       `json:"tokens"`
    LastTool       string    `json:"last_tool"`
    LastToolTarget string    `json:"last_tool_target,omitempty"`
    LastText       string    `json:"last_text"`                 // truncated to 200 chars
    LastUpdate     time.Time `json:"last_update"`
    ExitCode       int       `json:"exit_code,omitempty"`
    Error          string    `json:"error,omitempty"`
}
```

Written to `.ag/agents/<id>/activity.json` using **atomic rename** (`write tmp file → os.Rename`) to prevent corruption on crash/kill.

**Write frequency with rate limiting**:

| Event type | Action | Write trigger |
|------------|--------|---------------|
| `text_delta` | Append to `last_text` (keep last 200 chars) | Rate-limited: max once per 2s |
| `tool_execution_start` | Update `last_tool`, `last_tool_target` | Immediate |
| `tool_execution_end` | Update `last_tool` (append duration) | Immediate |
| `turn_end` | Increment `turns`, accumulate `tokens` | Immediate |
| `agent_start` | Set status = "running" | Immediate |
| `agent_end` | Set status = "done" | Immediate |
| stdout EOF | Set status = "done"/"failed" + exit code | Immediate |

This means:
- `ag agent status` works even without bridge running (reads from disk)
- After machine restart, historical status is available
- With bridge running, status is real-time (within 2s lag for text updates)

### Output Collection

The bridge accumulates all `text_delta` events into an internal buffer. When `ai` exits (agent_end or stdout EOF), the bridge writes the full accumulated text to `<agentDir>/output`.

`ag agent output <name>`:
- If agent is done/failed: reads from `<agentDir>/output` file, returns full text
- If agent is still running: returns error "agent still running, use `ag agent status` to check progress"
- Supports `--tail N` flag to return only last N lines

### Error Handling

When `ai --mode rpc` crashes or exits normally:

```go
// Event reader goroutine detects stdout EOF
func watchAgent(agentID string, cmd *exec.Cmd, stdout io.Reader) {
    // ... event reading loop ...
    
    // stdout closed = agent exited
    exitErr := cmd.Wait()
    activity := readActivity(agentDir)
    
    if exitErr != nil {
        if exitError, ok := exitErr.(*exec.ExitError); ok {
            activity.ExitCode = exitError.ExitCode()
            activity.Status = "failed"
        }
    } else {
        activity.Status = "done"
    }
    writeActivityAtomic(agentDir, activity)
    
    // Capture last 4KB of stderr
    stderrData, _ := os.ReadFile(filepath.Join(agentDir, "stderr"))
    if len(stderrData) > 4096 {
        stderrData = stderrData[len(stderrData)-4096:]
    }
    os.WriteFile(filepath.Join(agentDir, "stderr.tail"), stderrData, 0644)
}
```

When bridge itself crashes (tmux session dies):

- Bridge redirects its own stderr to `<agentDir>/bridge-stderr` for diagnostics
- `ag agent status <id>` detects stale state (see Stale State Recovery below)

## Solution

### Improvement 0: Spawn defaults to RPC mode via bridge

Delete headless mode. Delete the Python bridge script (`rpc_bridge.py`). Delete the bash watcher script.

`ag agent spawn` starts a tmux session running `ag bridge <id>`, which starts `ai --mode rpc` and holds pipes. CLI blocks until bridge.sock is ready, then returns.

### Improvement 1: Runtime intervention — steer / abort / prompt / kill / shutdown

| Command | How | Timeout |
|---------|-----|---------|
| `ag agent steer <id> "msg"` | Connect bridge.sock → send steer RPC → wait response | 5s |
| `ag agent abort <id>` | Connect bridge.sock → send abort RPC → wait response | 5s |
| `ag agent prompt <id> "msg"` | Connect bridge.sock → send prompt RPC → wait response | 10s |
| `ag agent kill <id>` | `tmux kill-session -t ag-<id>` | immediate |
| `ag agent shutdown <id>` | Connect bridge.sock → shutdown handshake | 30s, then kill |

**Progressive fault tolerance** (4 levels):
- `steer` — give hint, agent continues
- `abort` — cancel task, agent alive for retry
- `shutdown` — graceful handshake (agent can reject)
- `kill` — tmux kill-session, process dies

**Shutdown handshake** (via bridge.sock → RPC pipe):

1. CLI sends `{"type":"shutdown_request","requestId":"<uuid>"}` via bridge.sock
2. Bridge forwards to `ai` stdin via RPC pipe
3. Agent idle → RPC responds success → bridge tells agent to exit → agent exits cleanly
4. Agent busy → RPC responds failure → bridge returns `{"ok": false, "error": "agent busy"}`
5. Caller can retry later or escalate to `kill`
6. If no response within 30s, bridge returns timeout error, caller can `ag agent kill`

### Improvement 2: Structured status feedback

`ag agent status <id>` reads from `activity.json`:

```
status: running
uptime: 3m42s
turns: 7
last_tool: edit (pkg/auth/login.go)
last_text: "I'll fix the auth handler..."
tokens: 12400
pid: 12345
session: .ag/agents/worker-1/session.jsonl
```

`ag agent ls` shows one-line summary per agent:

```
ID        STATUS   UPTIME  TURNS  LAST_ACTIVITY
worker-1  running  3m42s   7      edit login.go
worker-2  idle     1m20s   3      (idle)
reviewer  done     5m01s   4      (finished)
```

Both commands support `--format json` for LLM consumption.

### Improvement 3: Structured task results

```bash
ag task done <id> --summary "implemented OAuth2 login, 3 files changed"
ag task fail <id> --error "rate limited by API" --retryable
```

`ag task show <id>` displays:

```
id: t001
status: done
claimant: worker-1
description: Implement OAuth2 login
summary: implemented OAuth2 login, 3 files changed
output: .ag/tasks/t001/output.md
turns: 12
tokens: 24500
duration: 4m32s
created: 2026-04-18 10:00:00
claimed: 2026-04-18 10:00:05
finished: 2026-04-18 10:04:37
```

When task completes, aggregate turns/tokens/duration from agent's `activity.json` and write to `task.json`.

## Final Command Table

```
ag agent   — agent lifecycle
  spawn --id <name> [--system @file] [--input "text"] [--timeout 10m] [--cwd <dir>]
  steer <name> "message" [--file path]
  abort <name>
  prompt <name> "message" [--file path]
  kill <name>
  shutdown <name>
  wait <name...> [--timeout 600]
  status <name> [--format json|text]
  ls [--format json|text]
  output <name> [--tail N]
  rm <name...>

ag task    — task management
  create <description> [--file spec.md] [--dep t001,t002]
  import-plan <file>
  list [--status pending|claimed|done|failed] [--format json|text]
  claim <id> [--as worker-1]
  next [--as worker-1]
  done <id> [--summary "description"]
  fail <id> --error "reason" [--retryable]
  show <id> [--format json|text]
  dep add|rm|ls

ag send    — send message to a channel (target is always a channel-name)
ag recv    — receive message from a channel (--wait blocks until message arrives)

ag channel — channel management
  create <name>
  ls
  rm <name>

ag bridge  — internal: bridge process (runs inside tmux, not called directly)
  <agent-id>
```

### Command Semantics

**`ag agent kill <name>`**: Terminates the tmux session (which kills both bridge and ai process). Does NOT delete agent files. Agent directory remains with final `activity.json` showing status "killed".

**`ag agent rm <name...>`**: Deletes agent directory entirely. Only works when agent is not running (status is done/failed/killed). Use `--force` to kill first then remove.

**`ag agent wait <name...> [--timeout 600]`**: Polls `activity.json` for each named agent (every 1s) until all reach a terminal state (done/failed/killed). Returns when all agents finish or timeout expires. On timeout, prints which agents are still running and exits with code 1.

### Agent ID Validation

Agent IDs must satisfy:
- Pattern: `^[a-zA-Z0-9_-]+$`
- Length: 1-64 characters
- Reason: must be valid in tmux session names (`ag-<id>`) and filesystem paths

### Removed Commands

| Old Command | Replacement | Reason |
|-------------|-------------|--------|
| `ag spawn --mode headless` | Deleted | RPC mode is the only mode |
| `ag spawn --mode rpc` (python bridge) | `ag agent spawn` → `ag bridge` in tmux | Replaced by Go bridge |
| `ag read` | `ag agent status` | Structured status replaces raw text |
| `ag stop` | `ag agent abort` / `ag agent kill` | Progressive fault tolerance |
| `ag team *` | Deleted | CWD provides natural isolation |
| `ag send <agent-id>` | `ag agent prompt <id>` | Direct agent messaging uses RPC pipe |

## Storage Layout

```
.ag/
  agents/
    worker-1/
      meta.json        ← spawn info: id, system_prompt, input, timeout, cwd, spawnedAt
      activity.json    ← event-stream state: status, turns, tokens, last_tool, last_text, pid (ai PID)
      output           ← final output text (written on agent exit)
      stderr           ← ai process stderr capture
      stderr.tail      ← last 4KB of stderr (written on agent exit)
      bridge-stderr    ← bridge process stderr (for diagnosing bridge crashes)
      bridge.sock      ← Unix socket (runtime, created by bridge process)
      session.jsonl    ← ai session file (if applicable)
    worker-2/
      ...
  tasks/
    t001/
      task.json        ← description, status, claimant, deps, summary, turns, tokens, duration
    t002/
      ...
  channels/
    task-queue/
      *.msg
    review-queue/
      *.msg
```

Note: No separate `status` file. Status is read from `activity.json` only. This avoids synchronization between two files.

## Communication Model

Two layers:

1. **Control plane** (leader → agent): `ag agent steer/prompt/abort/shutdown/kill`
   - CLI connects to agent's `bridge.sock`
   - Bridge forwards to `ai` stdin via RPC pipe
   - Immediate effect, low latency
   - `kill` uses `tmux kill-session`
   - Connection model: one request per connection (HTTP-style)

2. **Data plane** (agent ↔ agent): `ag send/recv` via file channels
   - Async, agent polls with `ag recv --wait`
   - Channel decouples sender and receiver
   - Multiple agents can listen to same channel

## Scope

**In scope:**
- Bridge-per-agent architecture (Go bridge process in tmux)
- Improvement 0-3 (rpc default + steer/abort/prompt + structured status + structured results)
- Command reorganization under `ag agent` / `ag task` subcommands
- Delete headless mode, Python bridge, bash watchers
- Delete `ag team` (not needed — storage follows CWD)
- Keep `ag send/recv` and `ag channel` for agent-to-agent async communication
- `--format json` on all status/ls/show commands

**Out of scope:**
- SKILL.md rewrite
- Pattern script updates (pair, pipeline, fan-out, parallel)
- Upper-level skill descriptions (workflow, implement)
- Changes to `ai` main code RPC protocol (already sufficient)

## Design Decisions

1. **Bridge-per-agent in tmux** — Each agent gets its own bridge process in its own tmux session. No single point of failure. No daemon lifecycle management. tmux provides process persistence and observability.
2. **`ag bridge <id>` is an internal subcommand** — Reuses the same Go binary. Not documented for direct use. Bridge reads spawn config from disk, starts ai, holds pipes, serves socket.
3. **No backward compatibility** — headless mode and Python bridge deleted entirely.
4. **Shutdown via RPC pipe** — not channel. Shutdown is a control-plane operation, routed through bridge.sock → ai stdin.
5. **No team subcommand** — storage follows CWD, directory isolation is natural.
6. **Channel is data-plane only** — `ag send` targets channels only. Direct agent messaging uses `ag agent prompt`.
7. **Event stream persisted to disk** — `activity.json` updated by bridge with atomic rename writes and rate limiting. Commands work without bridge (read from disk) or with bridge (real-time via socket).
8. **`--format json` everywhere** — LLM agents are the primary consumer, structured output is table stakes.
9. **Stderr captured** — `ai --mode rpc` stderr → `<agentDir>/stderr`. Bridge stderr → `<agentDir>/bridge-stderr`. Last 4KB of ai stderr kept on crash in `stderr.tail`.
10. **tmux session naming** — `ag-<agent-id>` for discoverability. `tmux ls | grep ag-` shows all managed agents.
11. **Agent ID validation** — `^[a-zA-Z0-9_-]+$`, max 64 chars. Must be valid in tmux session names and filesystem paths.
12. **No separate status file** — Status lives only in `activity.json`. Eliminates sync complexity.
13. **Spawn blocks until ready** — `ag agent spawn` polls bridge.sock (max 10s) before returning. Caller can immediately run steer/prompt after spawn.
14. **Kill ≠ rm** — `kill` terminates processes but preserves files for diagnostics. `rm` deletes files. `rm --force` does kill then rm.

## Stale State Recovery

When tmux session dies (bridge crash, machine restart):

1. `ag agent status <id>` reads `activity.json` (may show stale "running")
2. Checks if tmux session `ag-<id>` exists
3. If session gone:
   - Check if ai PID in `activity.json` is still alive
   - If PID dead → update `activity.json` to status "failed", note "bridge/session exited"
   - If PID alive (orphan ai process) → kill it, mark "failed"
4. `ag agent rm <id>` cleans up agent directory (only when not running)

When bridge is running but `ai` process died:

1. Bridge's event reader detects stdout EOF
2. Bridge updates `activity.json` to "done" or "failed" with exit code
3. Bridge writes `stderr.tail`
4. Bridge writes `output` (accumulated text)
5. Bridge keeps running but returns `{"ok": false, "error": "agent not running"}` on subsequent commands
6. `ag agent status` shows "failed" with error details

## Prerequisites

- **tmux** must be installed. `ag agent spawn` validates this upfront and produces a clear error if missing.
- **ai** binary must be in PATH. The bridge starts `ai --mode rpc` directly.

## Migration Note

Pattern scripts that use old commands will break:

| Old | New |
|-----|-----|
| `ag spawn --mode headless` | `ag agent spawn` |
| `ag read <id>` | `ag agent status <id>` |
| `ag stop <id>` | `ag agent abort <id>` or `ag agent kill <id>` |
| `ag output <id>` | `ag agent output <id>` |
| `ag status <id>` | `ag agent status <id>` |
| `ag ls` | `ag agent ls` |
| `ag kill <id>` | `ag agent kill <id>` |
| `ag rm <id>` | `ag agent rm <id>` |
| `ag wait <id>` | `ag agent wait <id>` |

This is out of scope for this design but documented for future reference.
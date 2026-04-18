# Feature: ag CLI Redesign

## Summary

Redesign the `ag` CLI from a fire-and-forget agent spawner (headless/tmux) to a control-capable orchestrator using bridge-per-agent processes in tmux. Each agent gets a Go bridge process that holds `ai --mode rpc` pipes and exposes a Unix socket, enabling mid-turn steering, abort, session reuse, structured status, and structured task results.

## User Stories

### US1: Spawn agent with RPC bridge (P1) 🎯 MVP

As a leader agent, I call `ag agent spawn worker-1 --input "fix the auth bug"` and it returns successfully. A tmux session `ag-worker-1` is created, running `ag bridge worker-1` which starts `ai --mode rpc`. I can immediately run `ag agent status worker-1` and see real-time activity.

**Independent test:** Spawn an agent, verify tmux session exists, verify bridge.sock exists, verify activity.json shows "running" with ai PID, verify `ag agent kill worker-1` terminates the session.

### US2: Mid-turn steering (P1) 🎯 MVP

As a leader agent, while worker-1 is running a task, I call `ag agent steer worker-1 "don't use library X, it has a known bug"` and the message reaches the running agent, influencing its next action.

**Independent test:** Spawn agent with a long-running task, call steer, verify response `{"ok":true}`, verify agent's subsequent behavior reflects the steer message.

### US3: Abort and retry (P1) 🎯 MVP

As a leader agent, when worker-1 is stuck or going wrong direction, I call `ag agent abort worker-1` to cancel the current task. The agent stays alive. I then call `ag agent prompt worker-1 "try a different approach: use Y instead"` to reuse the session.

**Independent test:** Spawn agent, wait for it to start working, call abort, verify status shows "idle", call prompt with new message, verify agent picks up new task.

### US4: Structured status (P1) 🎯 MVP

As a leader agent, I call `ag agent status worker-1` and get structured information: status, uptime, turns completed, tokens consumed, last tool used, and recent text. I call `ag agent ls` and see a one-line summary per agent.

**Independent test:** Spawn agent, let it run for a few turns, call `ag agent status`, verify turns/tokens/last_tool are populated. Call `ag agent ls`, verify multi-agent summary.

### US5: Graceful shutdown (P1) 🎯 MVP

As a leader agent, I call `ag agent shutdown worker-1`. If the agent is idle, it exits cleanly. If busy, the shutdown is rejected and I get an error. I can escalate to `ag agent kill worker-1` to force termination.

**Independent test:** Spawn agent, call shutdown while idle → verify clean exit. Spawn agent, call shutdown while working → verify rejection. Then call kill → verify forced termination.

### US6: Structured task results (P1) 🎯 MVP

As a leader agent, when a worker completes a task, I call `ag task done t001 --summary "implemented OAuth2, 3 files changed"`. Later I call `ag task show t001` and see structured info: description, summary, turns taken, tokens consumed, duration.

**Independent test:** Create a task, spawn agent to do it, mark done with summary, call `ag task show`, verify all fields populated including turns/tokens/duration aggregated from activity.json.

### US7: Kill preserves diagnostics (P2)

As a leader agent, after killing worker-1, I can still run `ag agent status worker-1` to see final state (status: killed, last activity). I can run `ag agent output worker-1` to see whatever output was accumulated before the kill. Files remain until I explicitly `ag agent rm worker-1`.

**Independent test:** Spawn agent, let it do some work, kill it, verify status shows "killed" with last_tool/tokens preserved. Verify output file has partial content. Verify rm deletes the directory.

### US8: Wait for multiple agents (P2)

As a leader agent, I call `ag agent wait worker-1 worker-2 --timeout 600` and it blocks until both agents reach a terminal state (done/failed/killed). If timeout expires, I see which agents are still running.

**Independent test:** Spawn two agents, call wait, verify it returns when both finish. Test timeout scenario with a long-running agent.

### US9: Agent output retrieval (P2)

As a leader agent, after worker-1 finishes, I call `ag agent output worker-1` and get the full accumulated text output. If agent is still running, I get an error suggesting `ag agent status` instead. I can use `--tail 50` to get only the last 50 lines.

**Independent test:** Spawn agent, wait for completion, call output, verify full text. Test --tail flag. Test calling output while agent is still running.

### US10: Task failure with retry signaling (P2)

As a leader agent, when a task fails, I call `ag task fail t001 --error "rate limited by API" --retryable`. Later I can query tasks with `ag task list --status failed` and see which are retryable.

**Independent test:** Create task, mark failed with --retryable, list failed tasks, verify retryable flag is present.

### US11: Stale state recovery (P2)

As a leader agent, after a machine restart or tmux crash, I call `ag agent status worker-1` and it detects the tmux session is gone, checks if ai PID is alive, updates status to "failed", and reports the stale state.

**Independent test:** Spawn agent, manually kill the tmux session (`tmux kill-session -t ag-worker-1`), call status, verify it detects stale state and reports "failed".

### US12: Channel-based agent communication (P3)

As a worker agent, I call `ag recv task-queue --wait --timeout 120` to block waiting for a message on the task-queue channel. Another agent calls `ag send task-queue "review the auth module"` and I receive the message.

**Independent test:** Create channel, send message from one agent context, receive from another, verify message content. Test --wait blocking.

## Functional Requirements

- FR-001: `ag agent spawn` MUST start a tmux session with `ag bridge <id>` that manages `ai --mode rpc`
- FR-002: `ag agent spawn` MUST block until bridge.sock is ready (max 10s) before returning
- FR-003: Agent IDs MUST match `^[a-zA-Z0-9_-]+$` and be max 64 characters
- FR-004: `ag agent spawn` MUST validate tmux availability and ai binary before proceeding
- FR-005: Bridge MUST create bridge.sock listener BEFORE starting ai process
- FR-006: Bridge MUST write activity.json with atomic rename (write tmp → os.Rename)
- FR-007: Bridge MUST record ai process PID (not bridge PID) in activity.json
- FR-008: Bridge MUST redirect its own stderr to `<agentDir>/bridge-stderr`
- FR-009: Bridge MUST redirect ai stderr to `<agentDir>/stderr`
- FR-010: Bridge MUST accumulate text_delta events into output file on agent exit
- FR-011: `ag agent steer/abort/prompt` MUST connect to bridge.sock, send RPC command, wait for response
- FR-012: Socket protocol MUST use newline-delimited JSON, one request per connection (HTTP-style)
- FR-013: `ag agent kill` MUST use `tmux kill-session` and NOT delete agent files
- FR-014: `ag agent rm` MUST only work on terminal states (done/failed/killed), `--force` kills first
- FR-015: `ag agent shutdown` MUST attempt graceful handshake via RPC pipe with 30s timeout
- FR-016: `ag agent status` MUST read from activity.json (works without bridge running)
- FR-017: `ag agent status` MUST detect stale state: check tmux session existence, check PID liveness
- FR-018: `ag agent ls` MUST show one-line summary per agent with status, uptime, turns, last activity
- FR-019: `ag agent wait` MUST poll activity.json (1s interval) until all named agents reach terminal state
- FR-020: `ag agent output` MUST return error if agent still running, full text if done/failed
- FR-021: `ag task done` MUST accept `--summary` field
- FR-022: `ag task fail` MUST accept `--error` and `--retryable` fields
- FR-023: `ag task show` MUST include turns/tokens/duration aggregated from agent activity.json
- FR-024: All status/ls/show commands MUST support `--format json`
- FR-025: activity.json write rate: text_delta updates max once per 2s; tool/turn events immediate
- FR-026: On ai exit, bridge MUST write last 4KB of stderr to `stderr.tail`
- FR-027: Headless mode, Python bridge, bash watchers, and `ag team` MUST be deleted
- FR-028: Commands MUST be reorganized under `ag agent` / `ag task` / `ag channel` subcommands

## Non-Functional Requirements

- NFR-001: `ag agent steer/abort` response time MUST be under 1s (socket + RPC roundtrip)
- NFR-002: `ag agent status` MUST respond in under 100ms (file read only)
- NFR-003: Bridge memory usage MUST stay under 50MB (just holding pipes and buffering events)
- NFR-004: One bridge crash MUST NOT affect other running agents (isolation)

## Out of Scope

- SKILL.md rewrite
- Pattern script updates (pair, pipeline, fan-out, parallel)
- Upper-level skill descriptions (workflow, implement)
- Changes to `ai` main code RPC protocol
- Backward compatibility with old command names
- `ag team` subcommand (storage follows CWD)

## Success Criteria

- SC-001: Leader agent can spawn, steer, abort, prompt, kill, and get status of agents
- SC-002: `ag agent status` shows turns, tokens, last_tool, last_text for running agents
- SC-003: `ag task show` includes structured summary, turns, tokens, duration
- SC-004: Bridge crash does not affect other agents
- SC-005: `tmux attach -t ag-<id>` shows live agent output
- SC-006: After `ag agent kill`, agent files remain for diagnostics until `ag agent rm`
- SC-007: All commands support `--format json` for LLM consumption

## Technical Context

- Current system: `ag` Go CLI (cobra-based), spawns agents via tmux + headless/python bridge
- Integration points: `ai --mode rpc` stdin/stdout JSON-RPC protocol
- Constraints: tmux required, ai binary in PATH, Unix domain sockets for IPC
- Key existing code to delete: `spawnHeadless()`, `spawnRPC()` with Python bridge, `internal/tmux/` shell scripts
- Key existing code to keep: task management, channel management, storage layer
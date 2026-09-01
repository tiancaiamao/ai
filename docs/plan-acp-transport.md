# Plan: Unify on ACP — protocol/transport separation, drop RPC

> Status: PLANNING (not started). This doc is self-contained: read it top to
> bottom, then start at Phase 1. No further research is needed — all facts below
> were verified against the code.

## Goal

Keep **one code path** for the agent kernel, expose it through **ACP** as the
single protocol, and separate **protocol** (ACP) from **transport** (stdio /
unix socket). Then retire the legacy NDJSON RPC protocol.

Requirements (all already work today; this is a refactor, not new features):
- Built-in TUI + external UI (AionUi) → ACP
- `ai send` — one agent drives a subagent the way a human would
- `ai watch` — a UI that can attach to a running agent anytime and see what happens

Design stance (decided): **do not over-engineer.** ACP is the main protocol
(minimal support, not the full spec). Where ACP is not enough, extend it with
`_`-prefixed custom `session/update` values (ACP's official extension mechanism).
No DB, no WebSocket, no attach/resume state machine, no v2/replayFrom cursor.

---

## Verified current state (do NOT re-investigate)

### Kernel is already unified
`pkg/rpc/rpc_app.go:128` `initEventEmitter(emit func(agent.AgentEvent))` is the
single event source. It does state tracking + persistence (messages.jsonl,
compaction snapshot, session workdir) — **all protocol-agnostic** — then calls
`emit(event)`. Persistence/`/resume`/session store are all in the kernel.

Two **translators** are currently attached to that single `emit`:
| translator | input | output | coverage |
|---|---|---|---|
| `acpServer.emit` (acp.go:~672) | `agent.AgentEvent` | ACP `session/update` | **4 kinds** |
| `server.EmitEvent` (rpc_server.go:214) | `agent.AgentEvent` | flat NDJSON | **all 17 kinds** |

The "code overlap" is **not** duplicated kernels — it's two translators. The fix
is: delete the RPC translator, extend the ACP translator, and move watch/send
onto ACP.

### Kernel event set (17 types, `pkg/agent/event.go`)
`agent_start, agent_end, turn_start, turn_end, message_start, message_end,
message_update, tool_execution_start, tool_execution_end, text_delta,
tool_call_delta, thinking_delta, compaction_start, compaction_end,
loop_guard_triggered, tool_call_recovery, error, llm_retry`

### ACP v1 `session/update` standard types (11)
`agent_message_chunk, agent_thought_chunk, available_commands_update,
config_option_update, current_mode_update, plan, session_info_update, tool_call,
tool_call_update, usage_update, user_message_chunk`
(schema: `pkg/rpc/testdata/acp_schema_v1.json`)

### Event → ACP mapping
| kernel event | ACP | status |
|---|---|---|
| text_delta | `agent_message_chunk` | ✅ done |
| thinking_delta | `agent_thought_chunk` | ✅ done |
| tool_execution_start | `tool_call` | ✅ done |
| tool_execution_end | `tool_call_update` | ✅ done |
| agent_end | (prompt response `stopReason`) | ✅ done |
| user message | `user_message_chunk` | ✅ done |
| commands | `available_commands_update` | ✅ done |
| model switch | `config_option_update` | ✅ done |
| usage | `usage_update` | ⚠️ standard, not wired |
| **compaction_start/end** | ❌ none | → `_compaction` |
| **error** | ❌ none | → `_error` |
| **llm_retry** | ❌ none | → `_llm_retry` |
| **loop_guard_triggered** | ❌ none | → `_loop_guard` |
| **tool_call_recovery** | ❌ none | → `_tool_call_recovery` |
| agent_start/turn_start/turn_end | — | drop (internal) |
| message_start/message_end | — | drop (internal bookkeeping) |

### Payload structs for the 5 custom events (`pkg/agent/event.go`)
- `CompactionInfo`: `Type`("major"/"mini"), `Auto`, `Before`, `After`, `Error`, `Trigger`, `Summary`
- `LoopGuardInfo`: `Reason`
- `ToolCallRecoveryInfo`: `Reason`, `Attempt`
- `LLMRetryInfo`: `Attempt`, `MaxRetries`, `Delay`, `ErrorType`, `Error`
- error: `AgentEvent.Error`, `AgentEvent.ErrorStack`

### ACP server is hard-bound to stdio (the refactor target)
`pkg/rpc/acp.go:148` `acpServer` holds `out io.Writer` + `mu sync.Mutex`;
`writeMessage` marshals one NDJSON line to `out`. `RunACP(..., input io.Reader,
output io.Writer, ...)` (acp.go:166) is called from `subcommand/acp/acp.go:47`
with `os.Stdin`/`os.Stdout`. **There is no transport abstraction** — to run ACP
over unix socket, one must be extracted first.

### `/resume` (RPC) == ACP `session/load`
Both = `GetSession(id) + setSession()`. ACP adds `replayHistory` (acp.go:408)
which replays persisted messages as `session/update`. `handleSessionLoad` is
acp.go:346. So cross-restart recovery already works and is already standard ACP.
**serve can die and be restarted + `session/load` to restore — no state machine
needed.**

### The three subcommands (current transport wiring)
- `run.RunSubcommand` / `run.ServeSubcommand` (`subcommand/run/run.go`) call
  `startServeProcess` which **spawns an `ai rpc` child process** + stdout pipe →
  `tui.EventBroadcaster` + a **unix socket server** (`tui.SocketServer`).
- `tui.SocketServer` (`subcommand/run/tui/socket.go`) speaks a **custom**
  protocol: `Command{Type:"prompt"|"abort"|"stream", Message, FromSeq}` →
  `Response{OK,Error,Data}`; `stream(fromSeq)` upgrades to a live NDJSON event
  stream from the broadcaster.
- `runSocketHandler` (run.go:461) forwards `prompt`→child stdin, `abort`→SIGTERM.
- `ai watch` (`subcommand/run/watch_model.go`): history via **events.jsonl file
  replay** (`mode:"replay"`, `readAllExisting`, run.go:~438), live via socket
  stream. `ai run` embedded TUI reads the in-process broadcaster (no replay).
- `ai send` (`subcommand/send/send.go`): dials socket, `Stream` then
  `SendCommand{prompt|steer}`.
- Top-level dispatch: `cmd/ai/main.go` (`rpc`/`acp`/`run`/`serve`/`watch`/`ls`/
  `send`/`kill`).

### Reference architectures (why this design is sound)
- **pi**: subagent = one-shot spawn of a child `pi`, fire-and-collect JSONL. No
  "attach to a background agent" concept. **No ACP at all.**
- **codex**: subagent = in-process thread (`Codex::spawn` + channels). app-server
  is a lib with pluggable transport (`--listen stdio/ws/unix`), protocol fixed.
- **claude-code**: teammates = in-process (AsyncLocalStorage) or process-based
  (tmux/iTerm2). Inter-agent messaging = **filesystem mailbox**
  (`.claude/teams/{team}/inboxes/{agent}.json` + lockfile).
- **aioncore** (Rust): team = independent ACP agent processes (stdio), DB-backed
  mailbox, WebSocket to frontend, `attach/resume` = re-spawn + session resume.

**None use "serve opens a unix socket, watch/send each connect" for inter-agent
messaging** — but that's about *persistent inter-agent messaging*, which we do
NOT need. Our socket is a *live channel to a running process*; history/recovery
is separate (session store + `session/load`). So the socket stays, it just speaks
ACP instead of the custom `Command` protocol.

---

## Target architecture

```
kernel initEventEmitter → agent.AgentEvent (single, 17 kinds)
        ↓
  [ONE translator: ACP emit]  →  ACP session/update (JSON-RPC 2.0)
        ↓                        (8 standard kinds + 5 `_` custom kinds)
   ┌────┴──────────────────────────┐
stdio                            unix socket          ← transport layer
(AionUi / external UI)           (TUI / ai watch / ai send)
```

| need | ACP method(s) | transport |
|---|---|---|
| TUI / external UI | `session/new` → `session/prompt` → `session/update` | stdio |
| `ai watch` (attach anytime) | `session/load` (history) → subscribe `session/update` (live) | unix socket |
| `ai send` (drive subagent) | `session/prompt` (send like a human) → wait `stopReason` | unix socket |
| abort | `session/cancel` | unix socket |

ACP extension rule: custom events use `_`-prefixed `sessionUpdate` values
(e.g. `{"sessionUpdate":"_compaction", ...}`). Standard ACP clients ignore
`_`-prefixed values; our TUI renders them. Protocol version stays ACP v1.

---

## Decisions (confirm before starting)

1. **Custom event names**: `_compaction`, `_error`, `_llm_retry`, `_loop_guard`,
   `_tool_call_recovery` (snake_case, `_`-prefixed). OK? (Alt: one `_diagnostic`
   with a `type` field.)
2. **Where the transport interface lives**: new `pkg/transport/` (recommended —
   general-purpose, not rpc-specific) vs `pkg/rpc/transport/`.
3. **Worktree branch name**: `refactor/acp-transport`. OK?

---

## Phases

### Phase 1 — Extend ACP emit (keep RPC; independent, minimal)
- [x] 1.1 Create worktree from `main` (do NOT work on `scratch0`/`main` directly)
- [x] 1.2 In `acpServer.emit`, add the 5 `_`-prefixed cases: `_compaction`,
      `_error`, `_llm_retry`, `_loop_guard`, `_tool_call_recovery`. Payload
      carried in a new `acpUpdate.Meta any json:"_meta,omitempty"` field (ACP
      extension mechanism). `_compaction` wraps `{status, info}` (start/end).
- [x] 1.3 ACP translation test `acp_extension_test.go` asserting the 5 emit
      correct `session/update` + `_meta` (structural, NOT schema-validated —
      they are extensions and intentionally non-conforming to v1)
- [x] 1.4 Existing ACP tests green (incl. `TestACPSchemaNotifications`) — 8
      standard kinds untouched
- [x] 1.5 VERIFIED NO-OP for Phase 1: TUI `event_parser.go` switches on the raw
      `evt.Type` (RPC flat NDJSON via broadcaster) and already has cases for all
      5 kinds (compaction_start/end, error, llm_retry, loop_guard_triggered,
      tool_call_recovery). The TUI does NOT consume ACP yet, so it already shows
      these. ACP `_` rendering moves to Phase 3 (3.3).

### Phase 2 — Extract transport interface (ACP decoupled from stdio) ✅
- [x] 2.1 Define `transport.Conn` (read JSON-RPC msg / write JSON-RPC msg /
      close) replacing `acpServer`'s `out io.Writer` + `run(input io.Reader)`
      — new `pkg/transport` package: `Conn` interface in `transport.go`
- [x] 2.2 Implement `stdioTransport` (wraps current stdin/stdout; **zero
      behavior change**) — `NewStdio(in, out)` in `stdio.go`; write mutex moved
      into the conn; reuses pkg/rpc `contextReader` for ctx cancellation
- [x] 2.3 Make `acpServer` consume `transport.Conn` (no concrete transport import)
      — dropped `out`+`mu`, added `conn transport.Conn`; `run()` loops
      `ReadMessage`, `writeMessage` calls `WriteMessage`
- [x] 2.4 Run ACP tests — stdio path behavior unchanged (pure-refactor gate)
      — pkg/rpc full suite green
- [x] 2.5 Implement `unixSocketTransport` (concurrent clients; reuse
      `tui/socket.go` listener mechanics) — `UnixSocket` in `unixsocket.go`
      (stale-file cleanup, chmod 0600, per-conn `socketConn`); covered by
      `transport_test.go` (stdio + socket round-trip + concurrent, `-race` clean)

### Phase 3 — watch/send/run onto ACP over socket
- [ ] 3.1 `ai send`: socket client sends ACP `session/prompt`, awaits
      `stopReason` (replace `Command{type:prompt}`)
- [ ] 3.2 `ai watch`: on connect send ACP `session/load` (history replay), then
      subscribe `session/update` (live)
- [ ] 3.3 `ai run` TUI: input → ACP `session/prompt`; events → ACP
      `session/update`. Rewrite `event_parser.go` to switch on the ACP
      `sessionUpdate` value instead of the raw `evt.Type`, and add rendering for
      the 5 `_`-prefixed values (`_compaction`/`_error`/`_llm_retry`/
      `_loop_guard`/`_tool_call_recovery`) reading their `_meta`. Reuse the
      existing per-kind render logic already present in the parser.
- [ ] 3.4 abort (watch/send) → ACP `session/cancel`
- [ ] 3.5 End-to-end: serve → watch full history, send drives, `watch --follow`
      incremental — match current behavior

### Phase 4 — Remove legacy RPC
- [ ] 4.1 Delete `server.EmitEvent` flat-NDJSON path + handlers only RPC used
- [ ] 4.2 Remove the public `ai rpc` subcommand (KEEP kernel `newRPCApp` /
      `initEventEmitter`)
- [ ] 4.3 Delete the custom `Command{type:prompt/abort/stream}` socket protocol
      (superseded by ACP)
- [ ] 4.4 Full regression: `go build` + all tests + manual serve/watch/send
      smoke
- [ ] 4.5 Update README/ARCHITECTURE: document protocol(acp) vs
      transport(stdio/socket) layering

---

## Order note
The user's stated order was "extend ACP → wire watch/send → clean RPC →
refactor layering." The real dependency is that **layering (Phase 2) must come
BEFORE wiring watch/send onto ACP (Phase 3)**, because ACP is currently bound to
stdio and cannot run over unix socket until the transport is extracted. Phase 1
is independent and safe first.

## Conventions
- Code / comments / commits in **English**; user-facing explanations in Chinese.
- Do **not** commit directly to `main` — use a worktree.
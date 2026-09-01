# Plan: Unify on ACP — protocol/transport separation, drop legacy RPC

> Status: IMPLEMENTED. Phases 1–4 are complete on branch
> `refactor/acp-transport-followup`. This document records the migration
> decisions, the resulting architecture, and the verification checklist.

## Goal

Keep one code path for the agent kernel, expose it through ACP as the single
public protocol, and separate protocol (ACP) from transport (stdio / Unix
socket). Earlier protocol and socket-command paths are retired.


Requirements:

- Built-in TUI and external UI use ACP.
- `ai send` drives a subagent through `session/prompt`.
- `ai watch` attaches to a running agent and receives history plus live updates.

Design stance: keep the ACP implementation minimal. Where ACP is not enough,
extend it with `_`-prefixed `session/update` values, ACP's extension mechanism.
No database, WebSocket, attach/resume state machine, or replay cursor is
needed.

## Resulting architecture

```
agent event emitter
        │
        ▼
  pkg/rpc ACP kernel
        │
        ▼
  pkg/transport
   ┌────┴──────────────┐
 stdio              Unix socket
 ai acp              ai serve
                     ▲  ▲  ▲
                     │  │  └─ ai send
                     │  └──── ai watch
                     └─────── ai run TUI
```

`pkg/rpc` owns ACP request handling, session/application state, persistence,
and translation of agent events into `session/update` notifications.
`pkg/transport` owns stdio and Unix-socket framing and connection management.
The agent kernel is shared by both transports.

## ACP event mapping

The agent emits internal lifecycle and streaming events. ACP exposes the
user-facing subset as follows:

| Agent event | ACP update |
|---|---|
| text delta | `agent_message_chunk` |
| thinking delta | `agent_thought_chunk` |
| tool execution start | `tool_call` |
| tool execution end | `tool_call_update` |
| completed assistant message | `usage_update` when usage is available |
| user message | `user_message_chunk` |
| available commands | `available_commands_update` |
| model switch | `config_option_update` |
| compaction start/end | `_compaction` |
| error | `_error` |
| LLM retry | `_llm_retry` |
| loop guard | `_loop_guard` |
| tool-call recovery | `_tool_call_recovery` |
| completed turn | `_turn_end` |

Internal bookkeeping events such as agent start, turn start, and message
construction events are not exposed as separate ACP updates.

Custom updates use `_`-prefixed names and carry implementation-specific data
in `_meta`. Standard ACP clients may ignore these extensions.

## Session and transport behavior

- `ai acp` runs an ACP server over stdio.
- `ai serve` runs the same ACP server over a Unix socket.
- `ai run` starts the server and uses an ACP client for its TUI.
- `ai watch` sends `session/load` to replay persisted history, then consumes
  live `session/update` notifications.
- `ai send` sends `session/prompt` and waits for the response's `stopReason`.
- Abort uses `session/cancel`.
- Session recovery uses standard ACP `session/load`; no separate resume
  protocol is required.

## Completed phases

### Phase 1 — Extend ACP events

- [x] Add the five diagnostic `_`-prefixed update types.
- [x] Add structural ACP extension tests.
- [x] Preserve standard ACP update behavior.

### Phase 2 — Extract transports

- [x] Define `transport.Conn`.
- [x] Implement stdio transport with unchanged framing behavior.
- [x] Make the ACP server transport-independent.
- [x] Implement concurrent Unix-socket transport and tests.

### Phase 3 — Move clients to ACP

- [x] Move `ai send` to `session/prompt`.
- [x] Move `ai watch` history and live updates to ACP.
- [x] Move the `ai run` TUI to ACP updates.
- [x] Move abort to `session/cancel`.
- [x] Verify serve/watch/send lifecycle behavior.

### Phase 4 — Remove legacy RPC

- [x] Remove the flat NDJSON event translator and legacy handlers.
- [x] Remove the public `ai rpc` subcommand while retaining `pkg/rpc`.
- [x] Remove the custom socket command protocol.
- [x] Update README and architecture documentation.
- [x] Run the regression, build, and smoke-test checklist below.

## Verification checklist

Run from this branch before committing or opening a PR:

```bash
make fmt
go build ./...
make test
git diff --check
git status --short
```

The E2E suite is opt-in and requires a configured live model:

```bash
make e2e
```

## Conventions

- Code, comments, and documentation use English.
- User-facing explanations use Chinese.
- Keep `pkg/rpc` as the ACP kernel; do not rename it solely for protocol naming.
- Do not commit directly to `main`.
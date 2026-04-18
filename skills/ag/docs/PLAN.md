# Implementation Plan: ag CLI Redesign

> **Status**: APPROVED ✅  
> **Total**: 17 tasks, ~48 hours  
> **Groups**: 6 (ordered by dependency)

## Dependency Graph

```
T001 ──┐   T002 ──┐
       ├── T003   │
T004 ──┤          │
       ├── T005   │
T006 ──┤          │
       └── T007   │
              ┌───┘
T008 ─────────┤
T009 ─────────┤
T010 ─────────┤
T011 ─────────┘
       │
T012 ──┤── T013
T014 ──┤
       │
T015 ──┤── T016
T017 ──┘
```

## Group 1: Foundation — Types, cleanup, old code removal (7h)

| ID | Title | Hours | Deps |
|----|-------|-------|------|
| T001 | Define bridge and activity types | 2h | — |
| T002 | Remove team package and simplify storage | 2h | — |
| T003 | Delete old spawn logic (headless, rpc, python bridge) | 2h | T001, T002 |

**Commit**: `refactor: define bridge types, remove team package, delete old spawn logic`

Key files:
- `internal/bridge/types.go` — NEW: AgentActivity, BridgeCommand, BridgeResponse, SpawnConfig
- `internal/team/` — DELETE entire package
- `internal/storage/storage.go` — Simplify (remove ReadStatus/WriteStatus)
- `internal/agent/agent.go` — Gut old spawn code, keep skeleton

## Group 2: Bridge — Core process management component (14h)

| ID | Title | Hours | Deps |
|----|-------|-------|------|
| T004 | Implement activity.json writer with atomic rename | 3h | T001 |
| T005 | Implement event stream reader for ai --mode rpc | 4h | T004 |
| T006 | Implement Unix socket server for bridge commands | 3h | T001 |
| T007 | Implement bridge main loop (process lifecycle) | 4h | T004, T005, T006 |

**Commit**: `feat: implement bridge-per-agent architecture (activity, event reader, socket, lifecycle)`

Key files:
- `internal/bridge/activity.go` — NEW: ActivityWriter with rate limiting and atomic rename
- `internal/bridge/eventreader.go` — NEW: Parse ai stdout event stream
- `internal/bridge/socket.go` — NEW: Unix socket command server
- `internal/bridge/bridge.go` — NEW: Main bridge lifecycle

## Group 3: Agent commands — Rewrite using bridge (11h)

| ID | Title | Hours | Deps |
|----|-------|-------|------|
| T008 | Rewrite spawn command to use tmux + bridge | 3h | T003, T007 |
| T009 | Implement agent steer/abort/prompt via socket | 2h | T006, T008 |
| T010 | Rewrite agent status with activity.json and stale detection | 3h | T004, T008 |
| T011 | Implement agent kill, shutdown, rm, output, wait | 3h | T009, T010 |

**Commit**: `feat: rewrite agent commands with bridge-per-agent architecture`

Key files:
- `internal/agent/agent.go` — REWRITE: Spawn using tmux + bridge
- `internal/agent/rpc_client.go` — NEW: Socket-based steer/abort/prompt
- `internal/agent/status.go` — NEW: Activity-based status with stale detection
- `internal/agent/lifecycle.go` — NEW: Kill, shutdown, rm, output, wait

## Group 4: CLI reorganization and wiring (4h)

| ID | Title | Hours | Deps |
|----|-------|-------|------|
| T012 | Reorganize CLI command tree under ag agent/task/channel | 3h | T008-T011 |
| T013 | Add bridge subcommand and wire main.go | 1h | T007, T012 |

**Commit**: `feat: reorganize CLI command tree, add bridge subcommand`

Key files:
- `cmd/root.go` — MAJOR REWRITE: New command tree
- `cmd/rm.go` — DELETE (merged into root.go agent subcommands)

## Group 5: Task enhancements (3h)

| ID | Title | Hours | Deps |
|----|-------|-------|------|
| T014 | Enhance task done/fail/show with structured results | 3h | T012 |

**Commit**: `feat: enhance task commands with structured results`

Key files:
- `internal/task/task.go` — ADD: summary, error, retryable fields

## Group 6: Testing — Unit and integration tests (10h)

| ID | Title | Hours | Deps |
|----|-------|-------|------|
| T015 | Unit tests for bridge components | 4h | T007-T011 |
| T016 | Integration tests for full agent lifecycle | 4h | T015 |
| T017 | Unit tests for updated task and channel packages | 2h | T002, T014 |

**Commit**: `test: add unit and integration tests for bridge architecture`

Key files:
- `internal/bridge/bridge_test.go` — NEW
- `internal/agent/agent_test.go` — NEW
- `internal/task/task_test.go` — UPDATE

## Risks

| Area | Risk | Mitigation |
|------|------|------------|
| ai RPC protocol | Event stream format may differ from assumptions | Defensive parsing, log unknown events, test against real ai early |
| tmux compatibility | Behavior varies across versions | Test with 3.2+, add version check, use standard commands only |
| Socket cleanup | Stale bridge.sock blocks new creation | Remove before listen, check tmux session to detect stale |
| Race conditions | activity.json read/write races | Atomic rename ensures complete reads |
| Process orphaning | Bridge crash leaves ai orphaned | Set process group, kill group on exit, PID liveness checks |

## Requirement Coverage

All 28 FRs mapped to tasks:

| FR | Task(s) | Description |
|----|---------|-------------|
| FR-001 | T008 | ag agent spawn |
| FR-002 | T010 | ag agent status |
| FR-003 | T009 | ag agent steer |
| FR-004 | T009 | ag agent abort |
| FR-005 | T011 | ag agent kill |
| FR-006 | T011 | ag agent shutdown |
| FR-007 | T011 | ag agent rm |
| FR-008 | T009 | ag agent prompt |
| FR-009 | T011 | ag agent output |
| FR-010 | T011 | ag agent wait |
| FR-011 | T011 | Kill preserves diagnostics |
| FR-012 | T007 | Shutdown via RPC pipe |
| FR-013 | T007 | Kill sends SIGTERM, kills group |
| FR-014 | T010 | Status from activity.json |
| FR-015 | T010 | Status stale detection |
| FR-016 | T008 | tmux session naming |
| FR-017 | T006 | Unix socket protocol |
| FR-018 | T012 | --format json |
| FR-019 | T004 | activity.json atomic rename |
| FR-020 | T004 | activity.json rate limiting |
| FR-021 | T005 | Event stream → activity |
| FR-022 | T005 | output file accumulation |
| FR-023 | T007 | stderr.tail capture |
| FR-024 | T007 | Bridge sends initial prompt |
| FR-025 | T013 | ag bridge internal subcommand |
| FR-026 | T008 | ID validation |
| FR-027 | T014 | task done --summary |
| FR-028 | T014 | task fail --error --retryable |
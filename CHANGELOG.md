# Changelog

Architecture decisions, major feature evolution, and the "why" behind changes.
Not a git log mirror — focus on what changed at the design level and why.

## Protocol Simplification: Removed steer/follow_up/abort RPC Commands (2026-07)

**Problem**: Four protocol-level command types (`prompt`, `steer`, `follow_up`, `abort`) were registered in the RPC server, but only `prompt` was ever sent to the subprocess stdin. The other three were dead code:

- `ai send --id` always sent `"prompt"` type to the Unix socket
- The socket handler (`runSocketHandler`) forwarded both `"steer"` and `"prompt"` as `"prompt"` to RPC stdin — the distinction was discarded before reaching the handler
- `"abort"` was handled at the socket layer (SIGTERM), not via RPC protocol
- Nobody sent `"follow_up"` at the protocol level at all

**Changes**:

1. **Removed protocol constants**: `CommandSteer`, `CommandFollowUp`, `CommandAbort` from `pkg/rpc/rpc_types.go`
2. **Removed protocol handlers**: `handleSteer()`, `handleFollowUp()`, `handleAbort()` from `pkg/rpc/rpc_app.go`
3. **Removed dead registration**: Three `app.server.Register(...)` calls in `registerHandlers()`
4. **Simplified socket handler**: Merged `case "steer", "prompt"` → `case "prompt"` in `runSocketHandler()`
5. **Cleaned up tests**: Deleted `handler_steer_followup_test.go`, removed `TestRPCAppAbort`
6. **Updated docs**: `docs/rpc-protocol.md` table, `docs/ai-agent-control.md` references

**Result**: Only `prompt` and `ping` remain as protocol-level commands. `/steer`, `/follow-up`, `/abort` still work as slash commands through the prompt channel — no user-facing functionality was removed.

## Further Simplification: Removed ping, Eliminated Handler Dispatch Map (2026-07)

**Problem**: After removing steer/follow_up/abort, `ping` was the only remaining protocol command besides `prompt`, but nothing in production code ever sent a `ping` command. The `handlers map[string]Handler` dispatch was over-engineered for a single command.

**Changes**:

1. **Removed `CommandPing`** — constant and registration deleted
2. **Removed `handlers` map + `Register()` + `HasHandler()`** from `Server` struct
3. **Added `promptHandler Handler` field + `SetPromptHandler()`** — direct reference instead of map lookup
4. **Simplified `handleCommand()`** — uses `if cmd.Type == "prompt"` directly instead of map dispatch; non-prompt commands fall through to slash command dispatch
5. **Simplified `NewServer()`** — no more ping pre-registration; empty initialization
6. **Removed `TestRPCAppPing`** smoke test
7. **Updated docs**: protocol table and CHANGELOG

**Result**: The entire RPC command dispatch is now a simple if-else ladder, eliminating 30+ lines of infrastructure code for a single protocol command type.

## Architecture: Package Structure Reorganization (2026-06)

**Problem**: Package structure didn't reflect the actual separation of concerns:
- `pkg/app` contained RPC application logic but the name was ambiguous
- `pkg/run` contained TUI-related code but was placed in `pkg/` (should be in `subcommand/`)
- `pkg/agent` contained 368 lines of untested metrics code

**Design principle**: `pkg/` should only contain RPC core logic; TUI and CLI implementations should be in `subcommand/`.

**Changes**:

1. **Deleted metrics (368 lines)**:
   - Removed `pkg/agent/metrics.go` and related files (`metrics_aggregate.go`, `metrics_snapshot.go`, etc.)
   - Removed metrics from `Agent`, `LoopConfig`, and `executeToolCalls`
   - Removed `TokenRateStats` type and token rate handling from RPC types
   - Metrics were untested and not integrated with core functionality

2. **Moved `pkg/app/` → `pkg/rpc/`** (23 files):
   - RPC application, handlers, types, session writer
   - Changed package name from `app` to `rpc` for clarity
   - Updated all import paths across codebase

3. **Moved `pkg/run/` → `subcommand/run/tui/`** (17 files):
   - TUI shared code (event renderer, socket server, metadata)
   - Created `subcommand/helpers/` for shared CLI utilities
   - Updated all import paths from `pkg/run` to `subcommand/run/tui`

**New structure**:
```
pkg/                    - RPC core logic only
  ├── rpc/             - RPC server, handlers, types (from pkg/app)
  ├── agent/           - Agent core logic
  ├── cli/             - CLI subcommand entry points (uses subcommand/run/tui)
  └── ...

subcommand/            - Subcommand implementations
  ├── helpers/         - Shared CLI utilities
  └── run/tui/         - TUI shared code (from pkg/run)
```

**Status**:
- All tests passing
- Binary compiles successfully
- `pkg/cli/` still contains subcommand entry points (uses `subcommand/run/tui`)
- Full `pkg/cli/` split to `subcommand/*` can be done as follow-up

**Benefits**:
- Clearer separation: `pkg/` is pure RPC core, `subcommand/` is CLI/TUI layer
- Better testability: RPC core can be tested independently
- Easier to understand: Package names match their responsibilities

**See also**: [docs/architecture.md](docs/architecture.md)

## Architecture: Code Organization Refactor

### cmd/ai → pkg/app + pkg/cli (2026-05)

**Problem**: All RPC handler logic lived in `package main` (cmd/ai), making it untestable.
cmd/ai had grown to 5700+ lines across 20+ files.

**Changes**:
- Moved all `rpc_*.go`, `session_writer.go`, `helpers.go` to `pkg/app/`
- Moved CLI subcommands (run/serve/ls/send/kill/watch) to `pkg/cli/`
- `cmd/ai/main.go` is now a thin 182-line entry point
- Added smoke tests that exercise the full RunRPC pipeline (coverage 6.8% → 44.5%)

### Checkpoint Manager Removal (2026-05)

**Problem**: The checkpoint system (`AgentContextCheckpointManager`) wrote `agent_state.json`
snapshots after compaction, but this added complexity with minimal benefit.

**Changes**:
- Deleted `pkg/agent/checkpoint_manager.go`
- Removed `EnableCheckpoint` from `LoopConfig`
- Removed `checkpointMgr` field from `rpcApp`, `loopState`
- Removed `updateCheckpointManager`, `saveCheckpointAfterCompaction`, `savePreCompactionCheckpoint`
- Session resume (`resume.go`) remains but is now a no-op (no snapshots written)

## Context Management: Four Generations

The compaction/context-management system went through four major rewrites, each driven by a fundamental shift in constraints.

### v0: Sliding Window + Summary (2026-02)

The original approach. Keep recent N messages, summarize the rest via LLM.
Tool outputs were progressively archived during compaction to save space.

Problem: at long sessions, the summary quality degraded and critical context was lost.

Key commits: `266fc05` optimize compact summary prompt, `b3160ec` archive tool results in compaction, `74caeca` recover from context-length errors via compaction.

### v1: LLM-Autonomous Context Management (2026-03)

**Design**: Let the agent decide its own context management. Provided three tools:
- `truncate_messages` — trim stale tool outputs
- `update_llm_context` — summarize task state into a persistent text injected into context
- `compact` — full summarization

The `llm_context` file acted as "working memory": current task, completed steps, next steps, key file changes, decisions.

**Why it failed**: Instruction compliance. Asking the model to simultaneously do its main task AND manage context split its attention badly. Reward/punishment mechanisms to force compliance made the cognitive burden worse.

Key commits: `4394172` (#1) truncate-compact hint mechanism, `c865673` rename to "LLM Context", `f2b8002` design doc.

Archived design: `docs/archive/` (context management tools no longer exist in code).

### v2: Isolated Context Management Mode (2026-04 — 2026-05)

**Design**: Completely separate normal mode from context-management mode.
Normal mode: standard system prompt, no context-management tools, no metrics injected.
When context usage exceeds threshold (e.g. 20%), switch to context-management mode with dedicated tools and prompt.

Additional improvement: tool-call pairing enforcement (`ensureToolCallPairing`) to prevent orphaned tool calls after compaction.

**Why it was replaced**: The rise of 1M context window models (DeepSeek V4, GLM 5.2). Two problems:
1. **Cache hostility**: Every context-management mode entry rebuilt the entire prefix (truncate modified old messages), destroying prefix cache. At 50x price difference (DeepSeek V4 flash), this was prohibitively expensive.
2. **Trigger thresholds wrong for 1M**: 20% of 1M = 200K tokens before triggering. By then context was already huge, making the context-management mode entry very expensive.
3. **No-op waste**: LLM could decide "do nothing" after the expensive mode switch.

Key commits: `56b26a3` rename mini compact to context management, `f0e29cd` context management document, `0ae4ce2` emit compaction_start before Compact().

Archived: `LLMContext`, `CacheMode`, `ContextManager` all removed in `b28a112` (#305).

### v3: LLMDecide + Cache-Friendly Compaction (2026-06 — current)

**Design**: Two key shifts:
1. **Cache-friendly**: Never modify historical messages. Compaction appends a summary as a new message, preserving prefix cache. The `buildCacheFriendlyLLMContext` function keeps the message stream byte-identical for cached prefixes.
2. **LLM-decides compaction**: Tiered thresholds (SoftThreshold → TierMedium → TierHigh → HardLimit) with LLM yes/no gate at interval boundaries. At hard limit, force compaction without asking.

Session persistence uses Proposal B: post-compaction messages saved to `compactions/compaction_NNNNN.jsonl` snapshot; `messages.jsonl` appends a `compaction` entry with `snapshotRef`. Append-only, never rewrites history.

Key commits: `d3c9162` (#273) cache-friendly message architecture, `017919d` (#300) reuse prefix cache in compaction, `b6545ad` (#299) unify through compactor.Compact(), `6f4623a` (#302) LLMDecideCompactor, `8b8cb75` (#304) append-only compaction, `b28a112` (#305) remove old context management system.

Design doc: `docs/archive/cache-friendly-message-architecture.md` (archived — CacheMode was removed in #305; `buildCacheFriendlyLLMContext` that remains is a simpler internal helper, not the dual-mode design).

### Checkpoint Dead Code Cleanup (2026-07)

**What**: Removed ~1000 lines of dead checkpoint reconstruction code and flattened `agent_state.json` from nested `checkpoints/checkpoint_NNNNN/` directories to the session root.

**Why**: The checkpoint system (checkpoint directories, journal, messages.jsonl duplication, `current` symlink, `checkpoint_index.json`) was a legacy from earlier context-management generations. After v3's compaction architecture, session messages live in `messages.jsonl` with compaction snapshots in `compactions/`. The checkpoint reconstruction path (journal replay) had zero production callers. The only active code was `agent_state.json` persistence (CWD, turn count, compaction counters) used by `LoadResumeState()`.

**Two-step cleanup**:
1. Removed dead reconstruction code: `journal.go`, `journal_io.go`, `reconstruction.go`, `messages.jsonl` duplication in checkpoints, `ContextSnapshot`, `Reconstruct()`, `AppendMessage()`, journal types.
2. Flattened `agent_state.json` to session root. Deleted `checkpoint.go` (checkpoint dir creation, symlink management), `checkpoint_index.go`, `snapshot.go`. `SaveAgentState` / `LoadAgentState` now read/write directly from `agent_state.json` in the session directory.

### Dead Code Cleanup (2026-07)

**What**: Removed ~380 lines of dead or no-op code across the agent, compaction, and CLI layers.

**Why**: Several metrics caches, config fields, and a legacy CLI dispatch path had no live consumers — they were written and persisted but never read to influence behavior.

**Removed**:
- `PromptMetrics` and `ContextMetrics` caches + aggregation: fed by trace events (`prompt_start/end`, `context_update_reminder`, `context_decision_reminder`) that were never emitted.
- 10 ghost trace event definitions in `traceevent/config.go`.
- `LargeContextThreshold` constant (never used).
- 4 unused Agent methods: `GetExecutor`, `AutoRetryEnabled`, `SetLLMRetryConfig`, `SetMaxTurns`.
- `ToolSummaryStrategy` config field: writable via `/set` and RPC, persisted, logged — but never read by the compactor. Removed from Config, RPC types, handler, and all call sites.
- `deprecatedModeDispatch`: legacy `--mode` flag dispatch that only forwarded to `ai rpc`.

## Multi-Agent Orchestration: From `ag` CLI to Skill-Based PGE

### ag CLI — Bridge-per-Agent (2026-04)

A standalone Go binary (`skills/ag/`) for multi-agent orchestration. Features: agent lifecycle (spawn/steer/abort/kill/status), task DAG scheduling, inter-agent channels.

Architecture: bridge-per-agent — each agent runs as a detached process with a Unix socket control plane, stream log, and event reader. No central daemon, no tmux dependency.

**Why removed**: 6k+ lines of Go to maintain. The task DAG abstraction was being replaced by PGE pattern. The core value (spawn/steer/kill) could be done directly via `ai` CLI subcommands.

Key commits: `765bb93` (#151) bridge-per-agent redesign, `c3ccb6e` (#164) observability overhaul, `84368ba` (#248) remove ag dependency from benchmark, `f67c22d` remove deprecated ag/plan/implement skills.

Archived design: `docs/archive/ai-agent-control.md`.

### PGE: Planner-Generator-Evaluator (2026-05)

**Design**: Three-agent orchestration pattern replacing rigid workflows:
- **Planner**: orchestrator, interacts with human, decomposes tasks dynamically
- **Generator**: execution layer, each task gets an independent generator agent
- **Evaluator**: clean-context agent for acceptance testing

Key advantage over workflow: dynamic task decomposition (not static DAG), strong self-healing (worst case: planner falls back to executing/verifying itself).

Inspired by the long-run harness exploration — workflow mode's fundamental dilemma: "the more rigorous the flow, the less flexible; the more flexible, the less reliable."

Key commits: `c90b146` (#242) PGE infrastructure, `6ac6352` (#245) skills for ai CLI subagent pattern, `e819b4b` (#275) slim down orchestrator prompt.

Design doc: `docs/ai-agent-control.md` (live — design still matches current implementation).

## Workflow System: Rise and Fall (2026-03 — 2026-05)

### Workflow Engine (2026-03 — 2026-04)

Code-driven state machine for feature development, bug fixes, etc. Templates for each task type. Skills called CLI scripts which updated real workflow state — a defense against models updating state via text.

**Why abandoned**: "The more rigorous the flow, the less flexible." Frameworks like GSD were too monolithic — couldn't use individual skills independently. Late-stage skills lacked isolated testing. Workflow-ctl state migrations were fragile.

Key commits: `4eb4724` (#146) decompose monolithic workflow, `773c17d` (#187) decompose into composable skills, `821bef2` remove wf skill ("the gate is useless now").

### Plan Format: YAML → Markdown (2026-05)

Migrated plan format from YAML (`tasks.yml`) to single-file Markdown after structured 3-round debate (proposer vs opposer). Key reason: LLM modification pass rate was much higher with Markdown — YAML indentation errors were too frequent.

Key commit: `3dbaf30` (#230) migrate plan format.

Archived: `docs/archive/plan-format-analysis.md`, `docs/archive/tasks.yml`.

## Skill System: Progressive Disclosure (2026-05)

**Problem**: All skills loaded into system prompt — at 20+ skills, this consumed too many tokens.

**Design**: Top-N high-frequency skills shown in system prompt; rest discoverable via `find_skill` tool. Usage tracking with time decay (168-hour half-life) auto-ranks skills. Cold start shows all visible skills capped at topN.

The `find_skill` tool accepts keyword search across name, description, aliases, use-when triggers, and categories.

Key commits: `66b78d4` (#212) progressive disclosure with usage stats, `454d85a` (#286) move skills to user message injection, `e9d94a7` (#287) merge skills+instructions into single prefix user message, `ff65a65` (#290) improve find_skill search.

Design doc: `docs/skill-progressive-disclosure.md` (live, still matches implementation).

## Agent Kernel/Shell Separation (2026-05)

Inspired by "Agentic Harness Engineering" (Fudan, 2025). Key finding: harness (prompt/memory/middleware) impacts performance as much as the model. Middleware value comes from structural defense (pipeline interception), not prompt persuasion.

**Implemented**: Hook system (`pkg/agent/hooks.go`) with three hook types: BeforeModelHook, AfterToolHook, AfterAgentHook. `agent.yaml` config loaded via `pkg/agentconfig/`. Middlewares registered globally in `pkg/middlewares/`.

Built-in middleware: `destructive_guard` — detects rm -rf / kill -9 / mkfs etc. in bash output.

Key commit: `8ac2f42` (#258) Agent Kernel/Shell separation.

Archived designs: `docs/archive/agent-harness-evolution.md`, `docs/archive/agent-harness-evolve-v1.md`, `docs/archive/agent-harness-evolve-step-by-step.md`, `docs/archive/evolve-directions.md`.

## Harness Auto-Evolution (2026-06)

Autonomous prompt-optimization loop. Runs benchmark tasks, analyzes failures, LLM modifies harness (system prompt, context management policy), re-runs. 4 iterations improved pass rate from 57% → 93%.

Planner pipeline with attribution and tool filtering. Agent debugger design for trace-level issue detection.

Key commits: `3ea8baa` (#279) autonomous prompt-optimization loop, `10529ea` (#278) evolve planner pipeline.

Archived designs: `docs/archive/planner-system-prompt.md`, `docs/archive/evolve-output-spec.md`, `docs/archive/agent-debugger-design.md`.

## Daemon Mode & CLI Subcommands (2026-04 — 2026-05)

Evolved from single-process RPC to multi-instance daemon architecture:
- `ai serve` — background daemon mode
- `ai ls` — list running/recent instances (idle/running status)
- `ai watch` — attach to running instance (TUI or --summary mode)
- `ai send` — send message to running instance (--wait for synchronous)
- `ai kill` — stop running instance (graceful or --force)

Subagent isolation via run ID tracking: each agent writes child IDs to `~/.ai/runs/<run_id>/subagent` file for safe cleanup.

Safety feature: block `tmux kill-server` at tool level to prevent agent self-destruction.

Key commits: `54b553f` (#170) daemon mode, `5bbf304` (#183) ai kill, `a75d013` (#276) subagent isolation, `4437d11` (#266) send --wait and /rewind.

## Role System: `--agent-config` → `--role` (2026-07)

**Problem**: `--agent-config` required manually crafting `agent.yaml` paths. There was no
discoverable way to share config across worktrees or define project roles.

**Design**: Replace `--agent-config <path>` with `--role <name>` which loads
`~/.ai/roles/<name>/agent.yaml`. Roles are symlinked from a shared worktree, making
them portable across clones. Session metadata persists the role for automatic recovery
on re-attach.

**Changes**:

1. **Role loading** (previously `--agent-config`): Unified in `newRPCApp` after session
   init, enabling resume recovery from `meta.Role`
2. **Session meta**: Added `SessionMeta.Role` + `GetMeta()` / `SetSessionRole()`
3. **Resume recovery**: Re-attaching to an existing session without `--role` restores
   the previously-used role from session metadata
4. **`--system-prompt` priority**: Now overrides role config's system prompt (was
   reversed: role config always won)
5. **Removed `prompt.TemplateForRole()`**: Orchestrator/validator templates live in
   `~/.ai/roles/` as `system_prompt.md` instead of being embedded
6. **Skill stats**: Per-role `skill-stats.json` auto-created on first use
7. **Validation**: `ai run/serve --role non-exist` exits early with an error (was:
   spawned rpc subprocess silently then TUI started)

Key commits: `c9eb5aa` (#338) --role flag, `fddee39` role validation,
`93e5bdf` auto-create skill-stats.json.

## Removed Features

| Feature | Introduced | Removed | Why |
|---------|-----------|---------|-----|
| `hashline` edit mode | `1c98230` 2026-03 | `f6dd3d5` (#294) 2026-06 | Dead code, unused |
| `win/` directory (editor integration) | `4748839` 2026-02 | `6e02661` (#194) 2026-05 | Extracted to standalone module, then abandoned |
| `ag` CLI (6k+ lines) | `765bb93` (#151) 2026-04 | `f67c22d` 2026-05 | Replaced by skill-based patterns (PGE, subagent) |
| `workflow` skills | `4eb4724` (#146) 2026-04 | `821bef2` 2026-05 | Too rigid, decomposed into composable skills |
| Context management tools (v1/v2) | `4394172` (#1) 2026-03 | `b28a112` (#305) 2026-06 | Cache-unfriendly, cognitive burden, replaced by LLMDecide |
| `PROJECT_CONTEXT` injection | — | `c6a5763` (#284) 2026-06 | Removed, not useful |
| Go MCP implementation | — | `bfcb2cf` 2026-03 | Replaced by mcporter skill |
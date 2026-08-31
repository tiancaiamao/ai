# Changelog

Architecture decisions, major feature evolution, and the "why" behind changes.
Not a git log mirror — focus on what changed at the design level and why.

## Runtime Guard Against Nested Subagents (2026-08)

**Problem**: A subagent could invoke `ai run` or `ai serve` again, allowing unbounded recursive delegation despite prompt and skill guidance.

**What changed**: `ai run` and `ai serve` now propagate an internal subagent-depth marker to their RPC child. A depth-one subagent is rejected before a new run directory or subprocess is created, while the top-level RPC agent remains able to create one level of subagents.

**Why**: Enforcing the one-level delegation invariant at the process launcher prevents recursive spawning at runtime instead of relying on model behavior.


## Model-Scoped Proxy Routing (2026-08)

**What changed**: Added an optional provider-level `proxy` setting in
`models.json`. Model API requests use that configured proxy. The existing OpenAI
Responses path retains its environment-based proxy fallback when no model
proxy is configured; other model paths otherwise make direct connections. The
standalone `ai login-codex` OAuth flow continues to use standard proxy
environment variables so authentication can be bootstrapped independently.

**Why**: A global proxy environment affects tool subprocesses and unrelated
network operations. Keeping proxy selection in the model provider config makes
runtime routing explicit while preserving a convenient proxy mechanism for the
interactive login flow.

## Shared Slash-Command Result Renderers (2026-08)

**Problem**: Slash-command results were rendered twice with diverging output: the TUI shape-sniffed response payloads client-side (`subcommand/run/tui/event_renderer.go`, ~700 lines), while ACP hosts — which have no TUI renderer of their own and just display text — needed server-side formatting (PR #384 added a parallel set of per-command renderers in `acp.go`). Same commands, two code paths, inconsistent formats.

**What changed**: All result rendering now lives in `pkg/rpc/render.go` behind `FormatCommandResult(command, data)`, adopting the TUI's output formats as the single baseline (main-branch display is unchanged). ACP passes the command name (resolving ambiguous shapes like `/resume` switch-vs-list); the TUI event stream keeps shape sniffing as fallback since slash-typed prompts answer with `command: "prompt"`. The TUI dropped its private renderer copies entirely — `event_renderer.go` is now a thin mapping onto the shared renderer.

**Why**: One format decision per command, one place to change it. Client-side sniffing could not be shared downward (import cycle) so the canonical implementation moved into `pkg/rpc`, which both consumers already depend on.

## ACP Agent Mode: `ai acp` (2026-08)

**Problem**: The agent was only reachable through the proprietary `rpc` protocol, which editor integrations must implement by hand. ACP (Agent Client Protocol, agentclientprotocol.com) is an emerging open standard for editor↔agent communication — Emacs agent-shell, Zed, and JetBrains IDEs all speak it as clients. Supporting it makes `ai` a drop-in subprocess agent for any ACP client without writing a per-editor bridge.

**What changed**: Added an `ai acp` subcommand that serves the ACP surface over stdio (JSON-RPC 2.0, newline-delimited — the same framing as `rpc` mode):

- `pkg/rpc/acp.go`: `RunACP()` + `acpServer` implementing `initialize`, `session/new`, `session/prompt`, `session/cancel`, plus `session/update` notifications translated from agent events (`message_update` → `agent_message_chunk`, `tool_execution_start/end` → `tool_call`/`tool_call_update`).
- Refactored `pkg/rpc/rpc_handlers.go`: extracted `setupAgent()` + `registerAllHandlers()` from `RunRPC` so both modes share one assembly path (agent, tools, sessions, slash commands). `initEventEmitter` now takes an emit callback so ACP can translate events instead of forwarding RPC events.
- Single-session model: `session/new` returns the app session id; `session/prompt` waits for the next `agent_end` event to answer with `stopReason` (cancellable via `session/cancel` + `ag.Abort()`).

**Deliberately out of scope**: `fs/*`, `terminal/*`, `session/load`, MCP transports, and embedded resources beyond a plain-text `resource_link` hint. Capabilities are not advertised, so conforming clients treat them as unsupported. `mcpServers` in `session/new` are accepted and ignored.

**Why**: Lightweight first — the minimum ACP surface that agent-shell needs to drive the agent in a live Emacs buffer. The agent's own tools (read/write/bash/grep) already cover file and shell work, so proxy tooling via ACP would duplicate machinery without benefit.

## Queued Manual Compaction at Agent Step Boundaries (2026-08)

**Problem**: `/compact` could compact the shared agent context directly from the RPC handler while the agent loop was running, allowing concurrent mutation of `RecentMessages`.

**What changed**: Running `/compact` requests are now queued atomically on the agent and consumed by the agent loop after `turn_end`, before the next model call or `agent_end`. Manual and automatic compaction use the same loop path and emit correctly marked events; idle requests retain the synchronous path.

**Why**: Serializing context mutation at loop step boundaries prevents concurrent access while preserving the existing compaction event and session persistence flow.



## Session Resume: agent_state.json Removed, CWD via meta.json (2026-08)

**Problem**: #314 deleted the checkpoint manager — the only writer of `agent_state.json` — but kept the reader (`LoadAgentState` → `LoadResumeState`). Since then no binary writes the file, so the resume path always ran its `os.IsNotExist` branch and silently did nothing. The consequence was a regression: the workspace CWD (the only AgentState field that cannot be recomputed from messages) was no longer restored on resume. Meanwhile `AgentState` itself carried 7 fields (`ActiveToolCalls`, `LastCheckpoint`, `LastTriggerTurn`, `TurnsSinceLastTrigger`, `TotalTruncations`, `TotalCompactions`, `LastCompactTurn`) plus `SessionID`/`CreatedAt`/`UpdatedAt` with zero production readers or writers — leftovers from the checkpoint era.

**What changed**:

- Deleted `pkg/context/checkpoint_io.go` (`SaveAgentState`/`LoadAgentState`/`saveAtomic`) and `pkg/agent/resume.go` (`LoadResumeState`) with their tests.
- Slimmed `AgentState` to the fields that are live: workspace/token stats (recomputed each turn by `injectRuntimeMeta`), `ToolCallsSinceLastTrigger` (LLMDecide ask interval), and the runtime-meta snapshot cache. `NewAgentState(cwd)` dropped the dead `sessionID` parameter.
- CWD persistence moved to session `meta.json`: `SessionManager.SetSessionWorkdir` writes `SessionMeta.CurrentWorkdir` on `agent_end`; `createBaseContext` restores it before building the system prompt (which embeds the CWD).
- `ToolCallsSinceLastTrigger` now resets to 0 on resume by design — worst case it delays the next LLM-decide ask; it self-heals.

**Why**: One persistence channel per concern. Messages and session metadata live in the session directory's `messages.jsonl`/`meta.json`; `AgentState` is derived state that should be recomputed, not persisted. Keeping the dead reader around implied a fallback that no longer existed.



## Reverted: Executor-Level ToolTimeout Cap (2026-08)

**Problem**: #361 wired up the previously-dead `Concurrency.ToolTimeout` config as an executor-level hard cap (`NewToolExecutorWithTimeout`), wrapping every tool call in `context.WithTimeout(ctx, toolTimeout)`. The cap is fundamentally incompatible with tools that manage their own timeouts:

- **It overrode bash's per-call `timeout: N`.** Any user with a nonzero `toolTimeout` in config (e.g. a config file written before the default changed to 0) had every command killed at the cap, no matter what the LLM requested — `timeout: 300` was silently capped at 30s.
- **It severed session-abort propagation.** Bash listens on the parent ctx to kill its command on session abort. Once the cap's deadline fired, the wrapped ctx was permanently done, so a *later* real abort could no longer reach a command still running under bash's own longer timeout.
- **It was redundant.** Every current tool either manages its own timeout (bash: 120s default or the LLM's `timeout: N`) or is a local sub-second operation (read/grep/write/edit). No tool needed an executor-level safety net.

**What changed**:

- **Removed the executor cap entirely**: deleted `NewToolExecutorWithTimeout` and the `toolTimeout` field from `concurrentToolExecutor`; `NewToolExecutor` is the only constructor.
- **Removed the `Concurrency.ToolTimeout` config field** (default, ToLoopConfig wiring, README example). Old configs with `"toolTimeout"` load fine — the field is silently ignored by JSON unmarshalling, which also clears the hazard for stale configs.
- **Reverted #363's bash guard** (`ctx.Err() == context.Canceled` check): with no executor deadline, the parent ctx is only ever canceled on a real abort, so bash's original simple listener (`<-ctx.Done(); cancel()`) is correct again.
- **Kept #361's pipe-drain fix** (idle-grace timer) — that was the real bug fix and is unaffected.

**Why**: The cap tried to be a generic safety net but could not distinguish tools with their own timeout handling. Patching around it (making bash ignore the deadline) left the abort-propagation gap above. Removing the cap restores correct semantics: bash's `timeout` parameter is authoritative, session abort always reaches the command, and there is no dead config left to be re-activated later.

## Bash Tool Hang Fix: Idle-Grace Pipe Drain + Wired-Up ToolTimeout (2026-08)

**Problem**: The bash tool could hang indefinitely on commands that background a process. `cmd.Wait()` returns as soon as the main shell exits, but a descendant (e.g. a subshell waiting on a daemon started with `cmd &`) keeps the stdout/stderr pipe write ends open, so EOF never arrives. The tool then blocked forever on `outputWG.Wait()`, and because the timeout check runs *after* the drain, neither the 120s default nor an LLM-set `timeout` could ever fire. In production this hung a tool call for 20.6 hours. Meanwhile `Concurrency.ToolTimeout` (default 30s) was dead config: it was only logged, never enforced.

**What changed**:

- **Bash drain is now bounded by an idle-grace timer** (250ms), mirroring the pi project's `waitForChildProcess` design. After the main process exits, the drain finalizes once the pipes fall idle: every received chunk re-arms the timer, so actively-writing descendants keep the tool reading, while a quiet holder of the pipe (a daemon the user just started) releases it after the grace period. The daemon itself is left running — it is not part of the command, and we do not kill it.
- **`Concurrency.ToolTimeout` is now enforced** as an executor-level hard cap via the new `NewToolExecutorWithTimeout`. Its default changed from 30 to **0 (disabled)**: a 30s cap would break bash's 120s default and the LLM's `timeout: 300` override. 0 = tools manage their own timeouts (pi philosophy); admins can set it explicitly as a safety net.
- Closed-read errors (`os.ErrClosed`) from our own pipe teardown are no longer reported as "stream read error".

**Why**: The old code conflated "main process exited" with "pipe closed". Backgrounding is a legitimate bash pattern (`server &`), so the fix must not kill the background process — it must stop *waiting* for it. The idle-grace approach keeps full output for normal commands, bounds the wait for backgrounding commands, and leaves the timeout path reachable (the deadline check now executes because the drain can no longer block forever).


## Model Capabilities: Bitmask → Single Vision Flag (2026-08)

**Problem**: The capability system introduced for vision filtering was over-engineered for a single boolean question. It added a `Capability` bitmask (`pkg/model`), a `capabilities` field + parser in `models.json`, an `llm.Capability` re-export layer, and a two-function filter API — roughly 870 lines — to answer "does this model support images?". Worse, the value had to propagate through three construction sites (`GetLLMModel`, `ApplyModelLimitsFromSpec`, the RPC model-switch handler); one of them was missed, so `llm.Model.Capabilities` was always 0 and **every model's images were stripped**, silently breaking vision.

**What changed**: Removed the bitmask machinery and followed the pattern used by the pi project (`Model.input.includes("image")`):

- `pkg/model` package deleted (`capability.go`, `capability_test.go`, `README.md`).
- `llm.Model.Capabilities Capability` → `llm.Model.SupportsVision bool` (runtime-derived, `json:"-"`).
- `ModelSpec.Capabilities` → `ModelSpec.SupportsVision`, derived from the existing `input` field (`"image"`/`"vision"`) in `LoadModelSpecs`. The `capabilities` JSON field is removed (it was new in this PR, no compatibility burden).
- `DetectUnsupportedContent` + `FilterMessagesForCapability` merged into one function `FilterUnsupportedContent(messages, supportsVision) ([]LLMMessage, int)`.
- Fixed the propagation bug: `ApplyModelLimitsFromSpec` and the RPC model-switch handler now copy `SupportsVision` from the spec.
- Applied the filter to the compactor's LLM calls (`buildCacheFriendlyLLMContext`, used by `askLLM` and `GenerateSummary`) as well — the agent loop's filtering is local-only, so compaction would otherwise send the same `image_url` parts to a text-only model.

**Why**: A boolean capability needs boolean plumbing. Keeping the value on `ModelSpec` (single derivation point) and copying it at the two construction sites that have a spec eliminates the "forgot to propagate" failure class. Unknown models default to text-only (matching pi's custom-model default), which keeps the resume-with-text-model scenario safe.

## Removed Compaction Ack Requirement (2026-07)

**Problem**: The `<agent:hint>` injected after compaction required the LLM to acknowledge with a `<compaction_ack>` tag before making tool calls. Analysis of real sessions showed this was ineffective — the LLM acknowledged in text while simultaneously calling tools, never actually pausing to reload skills or re-read docs.

**What changed**: Removed the ack enforcement code while keeping the `<agent:hint>` message itself (the 3 behavioral requirements: reload skills, follow constraints, re-read docs). The hint is still present in RecentMessages but no longer requires acknowledgment.

**Removed**: `maxCompactionAckReminders` constant, `compactionAckReminders` field, `checkCompactionHintAcknowledged()`, `newCompactionHintReminder()`, checkpoint check block in `loop.go`, `CompactionAckTag` from `compaction_hint.go` (file deleted, function moved back to `loop_state.go`).

**Why**: The ack pattern asks the LLM to self-enforce a behavioral constraint with no execution-layer verification. The canary mechanism already verifies context retention; forcing ack at the loop level adds complexity for no measurable benefit.

## Canary Context Retention Check for LLMDecide (2026-07)

**Problem**: LLMDecide compaction mode relied solely on token count heuristics to decide when to compact. Token count is an indirect measure — models can suffer "lost in the middle" degradation well before hitting context limits, or function perfectly even near limits depending on content distribution.

**Solution**: Added a canary-based context retention check to the LLMDecide `askLLM` flow:

1. Each `askLLM` call appends an agent-visible `<agent:canary value="..."/>` message to `RecentMessages` (after cleaning old canaries). The expected value is stored in `Compactor.canaryValue`.
2. On the next `askLLM` call, the LLM is asked to report the canary value from the conversation.
3. A correct answer → proceed with normal confirm/reject logic. An incorrect answer → context degraded → **force compaction** (overrides LLM decision).
4. After each `askLLM` call, old canaries are cleaned and a new one is appended for the next round.

The canary is appended (never inserted mid-list), so the provider prefix-cache for earlier messages is unaffected. The canary naturally sinks to the "lost in the middle" zone as tool call/result messages accumulate.

On compaction (`Compact`), all canary messages are removed and `canaryValue` is reset, starting a fresh retention cycle.

**Files**: New `pkg/compact/canary.go` + `canary_test.go`, modified `compact.go` (Compactor struct, Compact, askLLM).

**Design doc**: Updated `docs/context-management.md` and `pkg/compact/README.md`.

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

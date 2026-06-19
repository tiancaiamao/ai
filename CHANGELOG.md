# Changelog

Architecture decisions, major feature evolution, and the "why" behind changes.
Not a git log mirror — focus on what changed at the design level and why.

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

Design docs: `docs/context-management.md` (live), `docs/archive/cache-friendly-message-architecture.md` (original proposal).

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
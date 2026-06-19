# Documentation

> **Before committing**: scan the [Live Docs](#live-docs) table below.
> If your change touches a listed code area, verify the corresponding doc is still accurate.

## Live Docs

These documents describe the **current** codebase and must stay in sync.

| Document | Covers | When to Update |
|----------|--------|----------------|
| [`../README.md`](../README.md) | Project overview, CLI usage, config, tools, env vars | CLI commands changed; tools added/removed; config structure changed |
| [`architecture.md`](architecture.md) | Package structure, component diagram, system overview | Package added/removed/renamed; component relationships change |
| [`rpc-protocol.md`](rpc-protocol.md) | RPC command types, event types, socket protocol | New RPC command/event added; protocol format changed |
| [`session-format.md`](session-format.md) | JSONL entry types, session layout, lazy loading | Entry type added/removed; session storage format changed |
| [`context-management.md`](context-management.md) | Compaction, LLMDecide, token estimation | Compaction strategy changed; context management refactored |
| [`test-strategy.md`](test-strategy.md) | Test pyramid, test files, regression tests | Test structure changed; new test layer added |
| [`skill-progressive-disclosure.md`](skill-progressive-disclosure.md) | Skill ranking, topN selection, find_skill discovery | Skill formatting/usage tracking changed |
| [`ai-agent-control.md`](ai-agent-control.md) | Agent-controlling-agent via CLI (steer/watch/send) | RPC slash commands or watch modes changed |
| [`agent-harness-evolution.md`](agent-harness-evolution.md) | Kernel/shell separation, hook system design | Hook system or harness config changed |
| [`evolve-directions.md`](evolve-directions.md) | Auto-evolution methodology and stages | Evolve loop or pipeline changed |
| [`evolve-output-spec.md`](evolve-output-spec.md) | Evolve artifact file formats | Evolve output file structure changed |
| [`planner-system-prompt.md`](planner-system-prompt.md) | System prompt for evolve planner agent | Evolve planner prompt changed |
| [`../pkg/*/README.md`](../pkg/) | Package-level API, types, key files | Package API changed; files added/removed/renamed |

## ADRs

Architecture Decision Records — immutable historical decisions.

| ADR | Topic | Status |
|-----|-------|--------|
| [001](adr/001-rpc-first-design.md) | RPC-First Design | Accepted |
| [002](adr/002-code-driven-workflow.md) | Code-Driven Workflow Engine | Superseded — workflow system removed |
| [003](adr/003-tmux-subagent.md) | Tmux for Subagent Isolation | Superseded — bridge-per-agent architecture |
| [004](adr/004-agents-instruction-loading.md) | AGENTS.md Instruction Loading | Accepted |

## Archive

[`archive/`](archive/) contains historical design proposals and completed task specs.
These are not actively maintained — read for context only.
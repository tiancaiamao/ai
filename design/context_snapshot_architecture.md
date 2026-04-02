# Context Snapshot Architecture Design

## Overview

This document describes the context management architecture for the AI agent.

**Key Design Philosophy:**

- **Event Log + Snapshot**: Messages are immutable logs; the active context is a reconstructed snapshot
- **Two-Mode Operation**: Normal mode (task execution) and Context Management mode (context reshaping)
- **LLM-Driven Decisions**: System monitors triggers, LLM makes context management decisions
- **Structured Context**: Maintained as LLMContext + RecentMessages + AgentState, not linear message history
- **User-Wait Trigger**: When context management is triggered, user response is paused until management completes

## Architecture

```
Persistence Layer                 Memory Layer              LLM Input
────────────────                 ─────────────            ─────────
messages.jsonl ──apply logs───▶  ContextSnapshot  ──render──▶ Request
(immutable)      (incremental)     │

                                      │
  checkpoints/                      ┌─┴──────────────────────┐
  └── checkpoint_NNNNN/             │  • LLMContext          │
      ├── llm_context.txt          │  • RecentMessages      │
      └── agent_state.json         │  • AgentState          │
                                    └────────────────────────┘
```

### Snapshot Components

- **LLMContext**: Structured context maintained by the LLM via `update_llm_context` tool. Contains current task, completed steps, key files, decisions, and open issues.
- **RecentMessages**: The most recent N messages sent to the LLM. Older messages may be truncated to manage token usage.
- **AgentState**: System-maintained metadata: message counts, token estimates, tool call tracking, mode state.

## Two Operating Modes

### Normal Mode (default)

Standard conversation flow: user message → LLM response → tool calls → LLM response → repeat. The system uses normal mode system prompt and renders context normally.

### Context Management Mode (triggered)

When the trigger checker determines context management is needed, the agent enters context management mode:
1. **User waits**: The user's turn is paused
2. **LLM reshapes context**: Uses specialized tools (`update_llm_context`, `truncate_messages`, `no_action`)
3. **Returns to normal**: After management completes, user's turn resumes with reshaped context

## Trigger Conditions

The system monitors several signals to determine when context management is needed:
- **Token usage**: Approaching the model's context window limit
- **Message count**: Too many accumulated messages
- **Tool call density**: High rate of tool calls since last management cycle
- **Stale tool outputs**: Many tool outputs that are no longer relevant

Trigger urgency levels: `skip`, `low`, `medium`, `high`, `critical`.

## Context Management Tools

The LLM has access to specialized tools during context management mode:
- **update_llm_context**: Updates the structured LLM context (task tracking, key files, decisions)
- **truncate_messages**: Marks older messages for truncation to reduce token usage
- **no_action**: Indicates no context management is needed right now

## Event Sourcing Model

Messages are stored as an immutable event log (`messages.jsonl`). Truncation operations are also events — they mark messages as truncated without deleting them. The `ContextSnapshot` is constructed by replaying all events, allowing full history recovery.

## Observability

The system emits trace events for:
- Context management trigger checks and decisions
- Token estimation and usage trends
- Mode transitions (normal ↔ context management)
- Checkpoint creation and management

## Key Implementation Files

- `pkg/agent/loop_normal.go`: Conversation loop with mode switching
- `pkg/agent/turn.go`: Turn execution trampoline
- `pkg/context/`: Trigger checker, context types, snapshot management
- `cmd/ai/rpc_handlers.go`: RPC handlers for agent operations

## Migration Notes

The old `Agent` (legacy) and new `AgentNew` architectures coexist. The new architecture is used by `ai` command, while `claw` continues to use the legacy `Agent` until migrated.

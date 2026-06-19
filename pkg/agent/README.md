# pkg/agent

Agent loop, state machine, event streaming, and tool execution orchestration.

## Overview

The `agent` package is the runtime core of the `ai` system. It manages the agentic loop — the cycle of sending prompts to an LLM, receiving responses, executing tool calls, and streaming events back to the caller.

**Key responsibilities:**

- Run the LLM turn loop with configurable limits (max consecutive tool calls, per-name limits, total timeout)
- Execute tools with concurrency control via a semaphore-based executor
- Stream structured events (`AgentEvent`) through an event channel
- Manage checkpoints for session recovery
- Coordinate compaction and context-limit recovery

## Core Types

### Agent

```go
type Agent struct { ... }
```

Top-level agent struct. Holds an `AgentContext`, `LoopConfig`, event channel, current LLM stream, and follow-up queue. Created via `NewAgent()`, started with `Run()`.

### AgentEvent

```go
type AgentEvent struct {
    Type string `json:"type"`
    // ...type-specific fields
}
```

Discriminated union event type emitted during agent execution. Event types:

| Type | Description |
|------|-------------|
| `agent_start` / `agent_end` | Agent lifecycle boundaries |
| `turn_start` / `turn_end` | Per-LLM-turn boundaries |
| `text_delta` | Streaming text chunk from LLM |
| `thinking_delta` | Reasoning/thinking content chunk |
| `tool_call_delta` | Partial tool call (name + arguments) |
| `tool_execution_start` / `tool_execution_end` | Tool execution lifecycle |
| `message_update` | Full message snapshot |
| `error` | Error event |
| `checkpoint` | Checkpoint saved |
| `compaction` | Context compaction performed |
| `context_limit_recovery` | Context overflow recovery |
| `tool_call_recovery` | Malformed tool call recovery |
| `llm_retry` | LLM call retry |

### LoopConfig

```go
type LoopConfig struct {
    Model              llm.Model
    APIKey             string
    GetModel           func() llm.Model       // Dynamic model switching
    GetAPIKey          func() string          // Dynamic API key switching
    Executor           ToolExecutor           // Tool execution interface
    Compactors         []agentctx.Compactor   // Compaction chain
    ThinkingLevel      string                 // off, minimal, low, medium, high
    MaxConsecutiveToolCalls int               // Default: 6
    // ...
}
```

### ToolExecutor

```go
type ToolExecutor interface {
    Execute(ctx context.Context, tool agentctx.Tool, args map[string]interface{}) ([]agentctx.ContentBlock, error)
}
```

Interface for tool execution with concurrency control. Implemented by `concurrentToolExecutor` which uses a semaphore to limit parallel tool invocations.

## Event Stream Pattern

`EventStream[T, R]` is a generic push-based stream:

```go
stream := agent.Run(ctx, messages)
for event := range stream.Events() {
    // handle AgentEvent
}
result := stream.Result() // final []AgentMessage
```

The stream supports abort via `Push(agentEndEvent)` and cancellation through context.

## Checkpoint Manager

`AgentContextCheckpointManager` integrates with `AgentContext` to write journal entries for session recovery. It tracks turn count and message index to write periodic checkpoints.

## Key Files

| File | Description |
|------|-------------|
| `agent.go` | Agent struct, construction, Run(), event emission, stream management |
| `loop.go` | Main LLM turn loop with tool execution, retry, and recovery logic |
| `executor.go` | `ToolExecutor` interface and concurrent executor implementation |
| `event.go` | `AgentEvent` type and constructors for all event variants |
| `eventstream.go` | Generic `EventStream[T, R]` implementation |
| `checkpoint_manager.go` | Journal-based checkpoint integration |
| `hooks.go` | Agent lifecycle hooks |
| `loop_hooks.go` | Loop-specific hook implementations |
| `tool_exec.go` | Tool execution dispatch |
| `tool_guard.go` | Tool execution safety guards |
| `tool_output.go` | Tool output processing |
| `tool_call_normalize.go` | Tool call normalization |
| `tool_metadata.go` | Tool metadata |
| `tool_tag_parser.go` | Tool tag parsing |
| `llm_stream.go` | LLM stream consumption and event translation |
| `llm_stream_parse.go` | Parsing LLM streaming responses into events |
| `llm_retry.go` | Retry logic for LLM API errors |
| `llm_error_types.go` | Error classification for retries |
| `error_stack.go` | Error chain tracking |
| `metrics.go` | Metrics collection |
| `metrics_aggregate.go` | Metrics aggregation |
| `metrics_snapshot.go` | Metrics snapshots |
| `result.go` | Agent run result types |
| `resume.go` | Session resume logic |
| `runtime_meta.go` | Runtime metadata |
| `conversion.go` | Message type conversions |

## Dependencies

- `pkg/llm` — LLM client and types
- `pkg/context` — AgentContext, messages, tools
- `pkg/traceevent` — Tracing integration
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
| `message_start` / `message_end` | Per-message boundaries |
| `text_delta` | Streaming text chunk from LLM |
| `thinking_delta` | Reasoning/thinking content chunk |
| `tool_call_delta` | Partial tool call (name + arguments) |
| `tool_execution_start` / `tool_execution_end` | Tool execution lifecycle |
| `message_update` | Full message snapshot |
| `compaction_start` / `compaction_end` | Context compaction performed |
| `loop_guard_triggered` | Loop guard triggered (repeated tool calls) |
| `tool_call_recovery` | Malformed tool call recovery |
| `llm_retry` | LLM call retry |
| `error` | Error event |

### LoopConfig

```go
type LoopConfig struct {
    Model              llm.Model
    APIKey             string
    GetModel           func() llm.Model       // Dynamic model switching
    GetAPIKey          func() string          // Dynamic API key switching
    Executor           ToolExecutor           // Tool execution interface
        Compactor          agentctx.Compactor    // Context compression
    ThinkingLevel      string                 // off, minimal, low, medium, high, xhigh
    MaxConsecutiveToolCalls int               // Default: 6
    MaxToolCallsPerName      int              // Per-name tool call limit
    MaxTurns                int               // Max conversation turns (0=unlimited)
    ContextWindow           int               // Model context window (0=default 128000)
    EnableCheckpoint        bool              // Auto checkpoint creation (default true)
    Hooks                   *HookRegistry     // Lifecycle hooks
    DrainSteer              func() []QueuedMessage // Claims queued steering at each model-call boundary
    DrainEvent              func() []QueuedMessage // Claims queued machine events at each boundary
    AgentContextPrefix      string            // Skills + AGENTS.md prefix for cache
    // ... many more timeout/retry/tool-output fields
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

The agent uses `RunLoop()` which returns an `llm.EventStream[AgentEvent, []AgentMessage]`:

```go
stream := agent.RunLoop(ctx, messages, agentCtx, config)
for event := range stream.Iterator(ctx) {
    // handle event.Value (AgentEvent)
}
result := stream.Result() // final []AgentMessage
```

The `Agent` struct exposes events via a channel:

```go
agent := agent.NewAgentWithContext(model, apiKey, agentCtx)
go agent.Prompt("Hello")
for event := range agent.Events() {
    // handle AgentEvent
}
```

The stream supports abort via `Push(agentEndEvent)` and cancellation through context.

When the run's context is canceled mid-stream (steer/abort), `Iterator` returns on `ctx.Done()` while tail events the loop pushed afterwards — the salvaged `aborted` assistant message plus its `turn_end` — are still queued. `processPrompt` therefore calls `stream.WaitDone(eventDrainTimeout)` and processes `stream.DrainQueued()` before exiting, so the aborted message reaches `AgentContext` (and session persistence) instead of being dropped. Drained `agent_end` events are skipped: `abortCurrentStream` already emits its synthetic copy directly (a re-emit would duplicate the subscriber signal) and its pre-salvage snapshot would otherwise wipe a same-drain turn_end writeback; compaction still syncs via `EventCompactionEnd`.

## Queued Input Delivery

Two kinds of input can arrive while a run is busy, and both are delivered
only at a model-call boundary — never inside a tool call, so an in-flight
call is never cancelled or split from its result:

| Input | API | Loop hook | Bound |
|-------|-----|-----------|-------|
| Human steering | `Agent.Steer` | `LoopConfig.DrainSteer` | 1 turn |
| Machine event (background op result, `ai send --event`) | `Agent.EnqueueEvent` | `LoopConfig.DrainEvent` | 1 turn |

`runInnerLoop` claims steering first, then events, before each LLM call
(after `turn_start`, before pre-LLM compaction) and again immediately after a
compaction, since a compaction can block for seconds. When the agent is idle
both start a run instead of queueing. Both queues are bounded (64 each); a
full steer or event queue returns an error rather than dropping a message.

The cost of boundary delivery is that a run receives the message earlier than
its author may have planned and can act on it mid-plan. The alternative —
draining at the end of the run — leaves the wait unbounded (measured: 161s
across 22 turns, and 13m07s / 15m44s in two other runs).

Both sides of the queue are traced (`steer_enqueued`/`steer_injected`,
`event_enqueued`/`event_injected`), and the inject marker carries `waited_ms`
and the `path` that delivered the message: `boundary` (the loop injected it
before an LLM call) or `run_tail` (the run ended first and the message became
the next prompt in the chain). Every enqueue marker therefore has a
counterpart, and the wait between queueing and delivery is read from the
trace instead of inferred from spans. `DrainSteer`/`DrainEvent` return
`[]QueuedMessage` (text plus `EnqueuedAt`), which is what makes that number
available to the loop.

`turn_start` is emitted per loop iteration, so turns 2..N are marked too. A
turn abandoned before its `turn_end` — context-limit recovery, or an empty
model response — leaves a start unpaired on purpose: both exits skip
`turn_end`, because a `turn_end` with a nil message is how callers are told
the run failed. Pairing is guaranteed on the normal path, not on error exits.
`background_op` (phase `start`/`end`) marks the lifetime of a command detached
by `bash` auto-backgrounding.

## Checkpoint Manager

`AgentContextCheckpointManager` integrates with `AgentContext` to write journal entries for session recovery. It tracks turn count and message index to write periodic checkpoints.

## Key Files

| File | Description |
|------|-------------|
| `agent.go` | Agent struct, construction, `Prompt()`, `Events()`, stream management |
| `loop.go` | `LoopConfig`, `RunLoop()` entrypoint, main LLM turn loop |
| `loop_state.go` | `loopState` struct & extracted methods (compaction, tool calls, turns, cleanup) |
| `executor.go` | `ToolExecutor` interface and concurrent executor implementation |
| `event.go` | `AgentEvent` type and constructors for all event variants |
| `eventstream.go` | Generic `EventStream[T, R]` implementation |
| `checkpoint_manager.go` | `AgentContextCheckpointManager` — journal-based checkpoint integration |
| `hooks.go` | `HookRegistry` — agent lifecycle hooks (BeforeModel/AfterTool/AfterAgent) |
| `loop_hooks.go` | Loop-specific hook implementations |
| `tool_exec.go` | Tool execution dispatch |
| `tool_guard.go` | Tool execution safety guards (loop guard, consecutive limits) |
| `tool_output.go` | `ToolOutputLimits`, tool output processing, truncated-output offload to `<session-dir>/toolout/` |
| `tool_call_normalize.go` | Tool call normalization |
| `tool_metadata.go` | Tool metadata extraction |
| `tool_tag_parser.go` | Tool tag parsing |
| `llm_stream.go` | LLM stream consumption and event translation |
| `llm_stream_parse.go` | Parsing LLM streaming responses into events |
| `llm_prefix_cache.go` | Provider prefix-cache miss detection (`checkPrefixCache`, request fingerprinting) |
| `llm_retry.go` | Retry logic for LLM API errors |
| `llm_error_types.go` | Error classification for retries |
| `error_stack.go` | Error chain tracking with stack traces |
| `metrics.go` | `Metrics` collection struct |
| `metrics_aggregate.go` | Metrics aggregation from trace events |
| `metrics_snapshot.go` | Metrics snapshot types |
| `result.go` | `UsageStats`, `GetTotalUsage()` result types |
| `runtime_meta.go` | Runtime metadata injection (`injectRuntimeMeta`); runtime_state snapshots are frozen as persisted messages at turn boundaries (`runtimeStateTurnMessage`) so LLM requests stay append-only for provider prefix caching |


## Dependencies

- `pkg/llm` — LLM client and types
- `pkg/context` — AgentContext, messages, tools
- `pkg/traceevent` — Tracing integration
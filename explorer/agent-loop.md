# Explorer: Agent Loop Architecture (`pkg/agent/`)

## Overview
The `agent` package is the runtime core of the AI system. It manages the agentic loop — the cycle of sending prompts to an LLM, receiving responses, executing tool calls, streaming events, and performing context compaction. The architecture separates a top-level `Agent` facade from an inner `RunLoop` state machine.

## Tech Stack
- **Language:** Go
- **Key Dependencies:** `pkg/llm` (LLM client/types), `pkg/context` (AgentContext, messages, tools, Compactor interface), `pkg/traceevent` (tracing), `pkg/compact` (compaction implementation), `pkg/session` (session management)

## Project Structure
```
pkg/agent/
├── agent.go                  # Agent struct, construction, Prompt/Steer/Abort/FollowUp API
├── loop.go                   # RunLoop entry + runInnerLoop state machine (main loop body)
├── loop_state.go             # loopState struct (shared mutable state for inner loop)
├── executor.go               # ToolExecutor interface + concurrentToolExecutor (semaphore-based)
├── tool_exec.go              # executeToolCalls() — parallel dispatch, normalization, tracing
├── tool_guard.go             # toolLoopGuard — detects/prevents infinite tool call loops
├── tool_call_normalize.go    # Tool call normalization (name casing, arg coercion)
├── tool_tag_parser.go        # Detects incomplete XML-style tool calls in text/thinking
├── compaction_controller.go  # CompactionController — pre-request compaction at RPC layer
├── llm_stream.go             # streamAssistantResponse() — LLM call + runtime meta injection
├── llm_stream_parse.go       # Streaming chunk parsing (StreamChunkState)
├── llm_retry.go              # Retry logic with exponential backoff + jitter
├── llm_error_types.go        # LLM error classification (rate limit, context limit, etc.)
├── event.go                  # AgentEvent discriminated-union type + constructors
├── eventstream.go            # Generic EventStream[T, R] — push-based async stream
├── checkpoint_manager.go     # Journal-based checkpoint create/restore
├── runtime_meta.go           # Runtime telemetry injection (tokens, paths, CWD)
├── conversion.go             # Message type conversions (agentctx ↔ llm)
├── metrics.go                # Metrics collector
├── result.go                 # Result type
├── error_stack.go            # Error chain tracking
├── tool_metadata.go          # Tool metadata helpers
├── tool_output.go            # Tool output truncation
```

## Core Components

### Agent (Top-Level Facade)
- **File:** `agent.go`
- **Responsibility:** Public API surface — `Prompt()`, `Steer()`, `Abort()`, `FollowUp()`, `Events()`, `Compact()`, config setters
- **Key State:** mutex channel (`mu`) for serializing prompts, `eventChan` (buffered 100), `followUpQueue` (buffered 100), embedded `LoopConfig`, trace buffer
- **Concurrency:** Uses a channel-as-mutex pattern (`chan struct{}` with capacity 1) — only one `processPrompt` runs at a time. Follow-ups are drained sequentially after the current prompt completes.

### RunLoop + runInnerLoop (State Machine)
- **File:** `loop.go`
- **Responsibility:** The core LLM-turn state machine
- **Entry:** `RunLoop()` creates an `EventStream`, launches `runInnerLoop` in a goroutine with panic recovery
- **Loop Body (pseudocode):**
  ```
  forever:
    1. shouldStop? (ctx cancelled / maxTurns reached) → emit AgentEnd, return
    2. advanceTurn (increment counter)
    3. savePreCompactionCheckpoint (if compactor should trigger)
    4. performCompaction (pre_llm_threshold) — proactive compaction
    5. streamAssistantResponseWithRetry → msg, err
       - On context_length_exceeded → performCompaction(context_limit_recovery), continue
       - On other error → emit error, return
    6. Append msg to RecentMessages
    7. Update token usage in AgentState
    8. If stopReason == "error"|"aborted" → emit AgentEnd, return
    9. Sanitize non-success stopReasons
    10. processToolCalls(msg) → hasMore, toolResults
    11. If !hasMore:
        - Try malformed tool-call recovery → continue
        - Check for empty response retry → continue  
        - Break (conversation done)
    12. Emit TurnEnd, continue loop
  ```

### loopState (Inner Loop State)
- **File:** `loop_state.go`
- **Responsibility:** Holds all mutable state for a single loop run
- **Fields:** `config`, `agentCtx`, `stream`, `compactionRecs` (max 1), `turnCount`, `loopGuard`, `checkpointMgr`, `emptyRetries`, `malformedRecs`, `newMessages`
- **Key Methods:**
  - `shouldStop()` — checks ctx cancellation + maxTurns
  - `advanceTurn()` — increments turn counter
  - `savePreCompactionCheckpoint()` — snapshot before compaction modifies state
  - `performCompaction()` — iterates compactors, first success wins
  - `processToolCalls()` — full tool lifecycle: extract → guard check → dispatch → results → checkpoint

### Tool Execution Pipeline
- **Files:** `tool_exec.go`, `executor.go`, `tool_guard.go`
- **Flow:**
  1. `processToolCalls()` extracts tool calls from assistant message
  2. `toolLoopGuard.Observe()` checks for repeated identical tool calls
     - Soft feedback (first N attempts): returns error ToolResults to LLM for self-correction
     - Hard abort (after max feedback): sanitizes message, ends loop
  3. `executeToolCalls()` dispatches in parallel:
     - Normalizes tool names (case-insensitive)
     - Coerces arguments (string → map)
     - Builds execution plans
     - Dispatches via `WaitGroup` — each plan acquires semaphore slot
     - Collects outcomes, truncates output, builds ToolResult messages
  4. Results appended to `RecentMessages`, checkpoints saved for `update_llm_context`

### concurrentToolExecutor
- **File:** `executor.go`
- **Pattern:** Semaphore-based concurrency limiter
- **Config:** `maxConcurrent` (default 10), `queueTimeoutSec` (default 60)
- **Behavior:** Acquires semaphore slot → executes tool → releases. On queue timeout or ctx cancel → returns error.

### Compaction System
- **Files:** `compaction_controller.go`, `loop_state.go` (`performCompaction`)
- **Two layers:**
  1. **In-loop compaction** (`loopState.performCompaction`): triggered pre-LLM ("pre_llm_threshold") and on context overflow ("context_limit_recovery"). Iterates `config.Compactors[]`, first successful one wins.
  2. **Pre-request compaction** (`CompactionController.MaybeCompact`): triggered at RPC layer before prompt/steer. Uses session-level compaction API.
- **Compactor Interface** (from `pkg/context`):
  ```go
  type Compactor interface {
      ShouldCompact(ctx context.Context, agentCtx *AgentContext) bool
      Compact(ctx *AgentContext) (*CompactionResult, error)
      CalculateDynamicThreshold() int
  }
  ```
- **CompactionResult:** Summary, token counts, message counts, type ("major"/"mini"), truncated count, LLM context updated flag
- **Recovery limit:** `maxCompactionRecoveries = 1` — only one context-limit recovery per loop run

### Event Stream
- **File:** `eventstream.go`
- **Pattern:** Generic push-based stream `EventStream[T, R]`
- **Mechanics:** Producer pushes events → queue or direct delivery to waiting consumers. Terminal event detected by `isComplete` callback. `Iterator(ctx)` returns a channel that respects context cancellation.
- **Used as:** `EventStream[AgentEvent, []AgentMessage]` — terminal event is `agent_end`

### LLM Retry Logic
- **File:** `llm_retry.go`
- **Strategy:** Exponential backoff with ±20% jitter
- **Classification:** rate_limit (8 retries, 3s base), context_limit (no retry), server error (retry), client error (no retry), timeout (retry)
- **Events:** Emits `llm_retry` events to frontend with attempt/delay/error info

### Checkpoint Manager
- **File:** `checkpoint_manager.go`
- **Mechanism:** Journal-based (append-only log) + periodic snapshots
- **Triggers:** Pre-compaction, post-compaction (if LLM context updated), after `update_llm_context` tool
- **Restore:** Load latest checkpoint + replay journal entries

## Key Patterns

### Channel-as-Mutex (Agent Serialization)
**Location:** `agent.go:63` (`mu chan struct{}`)
```go
case a.mu <- struct{}{}:
    a.wg.Add(1)
    go func() {
        defer func() { <-a.mu }()
        // ... processPrompt + followUps
    }()
```
**Usage:** Ensures only one prompt runs at a time. Follow-ups drain sequentially within the same lock hold.

### Dynamic Model/Key Switching
**Location:** `agent.go` (constructor), `loop.go:76-84`
```go
a.LoopConfig.GetModel = func() llm.Model { return a.model }
```
**Usage:** Callback pattern allows `SetModel()` mid-run to take effect on next LLM call.

### Loop Guard (Progressive Escalation)
**Location:** `tool_guard.go`
```go
type toolLoopGuard struct {
    maxConsecutive     int
    feedbackCount      int
    maxFeedbackAttempts int  // default 2
}
```
**Usage:** Hashes tool name + args. On repeated calls: first gives feedback to LLM, then hard-aborts.

### Malformed Tool Call Recovery
**Location:** `tool_guard.go` (`maybeRecoverMalformedToolCall`)
**Usage:** When stopReason=tool_calls but no parsable call exists, or XML tool-call markup detected in text/thinking. Injects a repair message. Limited to 2 recoveries.

### Empty Response Retry
**Location:** `loop.go:296`
**Usage:** If LLM returns stop_reason=stop with no actionable content (no text, no tool calls — only thinking), retries up to 2 times.

### Runtime Meta Injection
**Location:** `runtime_meta.go`, `llm_stream.go`
**Usage:** Injects LLM context overview + runtime_state YAML as a user message BEFORE the last user message. Updated every turn.

## Dependencies
- **External:** none (stdlib only)
- **Internal:**
  - `pkg/llm` — LLM client, streaming, model types
  - `pkg/context` — AgentContext, messages, tools, Compactor interface, journal
  - `pkg/traceevent` — tracing spans + events
  - `pkg/compact` — compaction implementation
  - `pkg/session` — session management
  - `pkg/prompt` — thinking level normalization

## Key Findings
1. **Two-layer loop architecture**: `Agent` is the public facade (prompt queuing, event bridging, tracing). `RunLoop`/`runInnerLoop` is the state machine (LLM turns, tool dispatch, compaction, recovery). They communicate via `EventStream[AgentEvent, []AgentMessage]`.
2. **Parallel tool execution**: Multiple tool calls in a single LLM turn are dispatched concurrently via `WaitGroup` + semaphore. Results are collected by index and emitted in order.
3. **Three-tier compaction**: (a) Pre-LLM threshold check every turn, (b) Context-limit recovery on overflow error, (c) Pre-request compaction at RPC layer. In-loop compaction has a recovery limit of 1.
4. **Progressive loop guard**: Not a simple circuit breaker — gives the LLM 2 chances to self-correct before hard-aborting. This is a soft→hard escalation pattern.
5. **Rich recovery mechanisms**: Malformed tool call recovery (2 attempts), empty response retry (2 attempts), LLM retry with exponential backoff (configurable per error type), context-limit recovery via compaction.
6. **Checkpoint safety**: Pre-compaction checkpoints save state BEFORE compaction modifies it, so if the compaction LLM call crashes, progress is preserved. Post-compaction checkpoints only fire when LLM context was actually updated.
7. **Dynamic model switching**: `GetModel`/`GetAPIKey` callbacks allow changing model mid-conversation without restarting.

## Gotchas
- **Compaction recovery is limited to 1**: If context overflow happens twice in one loop run, the second one is a fatal error. This is intentional to prevent infinite compaction loops.
- **Event channel is buffered at 100**: Events are dropped (with warning log) if the consumer can't keep up. This prevents slow consumers from blocking the loop.
- **Follow-up queue is buffered at 100**: `FollowUp()` returns error if queue is full.
- **`runLoopFn` is overridable**: The Agent stores `runLoopFn` as a function field, defaulting to `RunLoop`. Tests inject mocks. Production should always use the real `RunLoop`.
- **Channel-as-mutex isn't a real mutex**: `Prompt()` has a timeout (`agentBusyTimeout`) waiting to acquire the lock. If the agent is busy too long, it returns an error.
- **`processPrompt` drains follow-ups synchronously**: After one prompt completes, all queued follow-ups run sequentially. A follow-up that generates many tool calls could delay the next external Prompt.
- **`streamAssistantResponseFn` is a package-level var**: Overridden in tests. This is a test seam, not a production concern.

## Completeness Checklist

### Packages / Modules
- [x] `pkg/agent` — Agent loop, state machine, event streaming, tool execution
  - [x] `agent.go` — Agent struct, Prompt/Steer/Abort/FollowUp API
  - [x] `loop.go` — RunLoop + runInnerLoop (main state machine)
  - [x] `loop_state.go` — loopState (shared mutable loop state)
  - [x] `executor.go` — ToolExecutor interface + concurrentToolExecutor
  - [x] `tool_exec.go` — executeToolCalls parallel dispatch
  - [x] `tool_guard.go` — toolLoopGuard + malformed recovery + non-success sanitization
  - [x] `tool_call_normalize.go` — Tool call normalization
  - [x] `tool_tag_parser.go` — Incomplete tool call detection
  - [x] `compaction_controller.go` — Pre-request compaction at RPC layer
  - [x] `llm_stream.go` — LLM streaming + runtime meta injection
  - [x] `llm_stream_parse.go` — Stream chunk parsing
  - [x] `llm_retry.go` — Retry with exponential backoff
  - [x] `llm_error_types.go` — Error classification
  - [x] `event.go` — AgentEvent types + constructors
  - [x] `eventstream.go` — Generic EventStream[T, R]
  - [x] `checkpoint_manager.go` — Journal-based checkpoints
  - [x] `runtime_meta.go` — Runtime telemetry injection
  - [x] `conversion.go` — Message type conversions
  - [x] `metrics.go` — Metrics collector
  - [x] `result.go` — Result type
  - [x] `error_stack.go` — Error chain tracking
  - [x] `tool_metadata.go` — Tool metadata helpers
  - [x] `tool_output.go` — Tool output truncation

### Public API Surface
- [x] `Agent.Prompt(message)` — Send user message, wait for completion
- [x] `Agent.Steer(message)` — Interrupt current + send new message
- [x] `Agent.Abort()` — Cancel current execution
- [x] `Agent.FollowUp(message)` — Queue message for after current prompt
- [x] `Agent.Events()` — Event channel subscription
- [x] `Agent.Compact(compactor)` — Manual compaction
- [x] `Agent.Wait()` — Block until current prompt finishes
- [x] `Agent.Shutdown()` — Graceful shutdown
- [x] `Agent.SetModel/GetModel/SetAPIKey/GetAPIKey` — Dynamic config
- [x] `Agent.SetContext/GetContext` — AgentContext management
- [x] `Agent.AddTool/SetExecutor/SetCompactor` — Tool/executor setup
- [x] `Agent.SetToolOutputLimits/SetToolCallCutoff` — Output limits
- [x] `Agent.SetThinkingLevel/SetAutoRetry/SetMaxTurns/SetContextWindow` — LLM config
- [x] `Agent.GetMessages/GetState/GetMetrics/GetPendingFollowUps` — State queries
- [x] `RunLoop()` — Loop entry point (called by Agent internally)
- [x] `CompactionController.MaybeCompact()` — Pre-request compaction
- [x] `CompactionController.RestoreContext()` — Branch resume context restore
- [x] `NewToolExecutor(maxConcurrent, queueTimeoutSec)` — Create executor
- [x] `DefaultLoopConfig()` — Default configuration

### Key Behaviors
- [x] Multi-turn LLM loop with tool execution (runInnerLoop)
- [x] Concurrent tool execution via semaphore (WaitGroup + buffered channel)
- [x] Tool call normalization (case-insensitive name matching, arg coercion)
- [x] Progressive loop guard (soft feedback → hard abort after N attempts)
- [x] Malformed tool call recovery (detect XML markup in text/thinking, inject repair)
- [x] Empty response retry (stop but no actionable content)
- [x] LLM retry with exponential backoff + jitter (rate-limit aware)
- [x] Pre-LLM compaction (threshold check every turn)
- [x] Context-limit recovery compaction (on context_length_exceeded error)
- [x] Pre-request compaction (at RPC layer before prompt/steer)
- [x] Journal-based checkpointing (pre-compaction + post-compaction + post-tool)
- [x] Dynamic model/API key switching via callbacks
- [x] Runtime meta injection (LLM context + telemetry YAML before last user msg)
- [x] Non-success stop reason sanitization (network_error, rate_limit, timeout, etc.)
- [x] Panic recovery in RunLoop goroutine
- [x] Event stream with dynamic queue expansion
- [x] Tracing with per-prompt trace rotation

### Cross-cutting Concerns
- [x] Error handling: ErrorStack wrapping, classified LLM errors, structured error events
- [x] Logging: `slog` structured logging throughout, with turn/attempt context
- [x] Observability: `traceevent` spans and events for LLM calls, tool execution, compaction
- [x] Configuration: `LoopConfig` with defaults, embedded in Agent, dynamic callbacks
- [x] Concurrency: Channel-as-mutex for agent serialization, semaphore for tool concurrency, WaitGroup for parallel dispatch
- [x] Metrics: Dedicated `Metrics` type with trace buffer sink, metric invalidation on events
# Explorer: Tool Call Correction, Loop Guard & Cache Hit Rate Impact

## Overview
The agent loop (`pkg/agent/`) contains multiple mechanisms that detect, correct, or recover from malformed/stuck tool calls. Each mechanism either **modifies messages in-place** (cache-friendly) or **appends/inserts new messages** (shifts prefix, breaks prompt cache). Non-deterministic tool call IDs further reduce cache hit rates.

## Tech Stack
- Go 1.x, custom agent loop (no framework)
- LLM streaming via `pkg/llm`
- Context types in `pkg/context`

---

## Core Components

### 1. Tool Call Normalization
**File:** `pkg/agent/tool_call_normalize.go`
**Responsibility:** Fix malformed tool calls from LLM before execution.
**Cache Impact:** IN-PLACE modification — does NOT shift prefix.

| Function | What it does |
|----------|-------------|
| `normalizeToolCall()` | Master normalizer: strips HTML from name, maps aliases, unwraps `{"properties":{}}`, infers tool from args, ensures ID |
| `normalizeToolCallName()` | Strips HTML tags, removes control chars, trims, maps common aliases (`read_file`→`read`, `write_to_file`→`write`, etc.) |
| `ensureToolCallID()` | Generates ID if empty: `fmt.Sprintf("tool_%d_%d", time.Now().UnixNano(), seq)` |
| `inferToolFromArgs()` | If name is generic (`tool`, `function`, etc.), infers real tool from args keys |
| `unwrapPropertiesArguments()` | If args wrapped in `{"properties":{...}}`, unwraps to inner map |

**Cache Gotcha:** `ensureToolCallID()` generates **non-deterministic IDs** using `time.Now().UnixNano()` + atomic counter. Even if the LLM returns the same tool call, the corrected ID will differ each run, making the serialized message content non-deterministic.

### 2. Tool Tag Parser (XML-style Recovery)
**File:** `pkg/agent/tool_tag_parser.go`
**Responsibility:** Parse tool calls from `<read>`, `<bash>`, `<edit>`, etc. tags in assistant text/thinking content.
**Cache Impact:** IN-PLACE modification of the assistant message's Content blocks.

| Function | What it does |
|----------|-------------|
| `injectToolCallsFromTaggedText()` | Scans assistant message text for XML tool tags, extracts them, replaces text with `ToolCallContent` blocks |
| `injectToolCallsFromThinking()` | Same, but scans `<thinking>` content and appends extracted tool calls to the message |
| `parseLooseArgKeyValueToolCall()` | Parses `arg: value` style tool calls from text |

**Called from:** `llm_stream.go` line ~225, on the final assistant message before emitting it.

**Cache Gotcha:** `buildToolCallMessage()` generates IDs via `fmt.Sprintf("tool_%d_%d", time.Now().UnixNano(), i)` — also **non-deterministic**.

**Behavior:** Skips if message already has valid tool calls. Modifies `msg.Content` in-place (replaces text with ToolCallContent blocks). Does NOT append new messages to history.

### 3. Malformed Tool Call Detection & Recovery
**File:** `pkg/agent/tool_guard.go`

#### Detection: `detectMalformedToolCall()` and `shouldRecoverMalformedToolCall()`
Checks for:
- `stop_reason == "tool_calls"` but no parsable tool calls
- Text or thinking contains `<tool_call`, `<tool>`, `ErrorException`, or ` excer ` without a valid parsed tool call
- `DetectIncompleteToolCalls()` finds unclosed/extra tags, uppercase tool names

#### Recovery: `maybeRecoverMalformedToolCall()` → `buildMalformedToolCallRecoveryMessage()`
**Cache Impact:** APPENDS a new user message — shifts prefix.

**What happens:**
1. Increment `malformedRecs` counter (max `defaultMalformedToolCallRecoveries = 2`)
2. Create a USER message with:
   - `Role: "user"`
   - `Kind: "tool_call_repair"`
   - `Visibility: (true, false)` (agent-visible, user-hidden)
   - Text: `"[Tool-call recovery, attempt N] Your previous response attempted a tool invocation but the tool call format was invalid (reason). Re-emit the intended call..."`
3. Append to `agentCtx.RecentMessages` AND `state.newMessages`
4. Loop continues (does NOT break)

**Position:** Appended at the end of `RecentMessages` (not inserted mid-conversation).

**Constant:** `defaultMalformedToolCallRecoveries = 2` — after 2 recoveries, the loop ends.

### 4. Loop Guard (Infinite Loop Prevention)
**File:** `pkg/agent/tool_guard.go`

#### `toolLoopGuard` struct
Tracks consecutive calls with the same signature (tool name + args SHA-256 hash). Resets counter when tool/args change.

**Constants:**
- `defaultLoopGuardMaxConsecutive = 6` — consecutive same-sig calls before triggering
- `defaultLoopGuardMaxFeedback = 3` — feedback rounds before hard abort

#### When triggered — Soft Feedback (before maxFeedback):
**Cache Impact:** APPENDS toolResult messages — shifts prefix.

Creates `ToolResult` messages (role="toolResult", IsError=true) with actionable feedback:
```
[Loop guard] Repeated identical tool call detected: <reason>
You have made the same tool call with identical arguments multiple times...
Feedback attempt N of M. If you continue with the same call, the loop will be terminated.
```
- Appended to `agentCtx.RecentMessages` AND `state.newMessages`
- `hasMore` stays true — LLM gets another turn with feedback

#### When triggered — Hard Abort (after maxFeedback):
**Cache Impact:** IN-PLACE modification of last assistant message.

1. `sanitizeMessageForToolLoopGuard()` strips all `ToolCallContent` blocks from the message
2. Appends a `TextContent` block with: `[Loop guard] Stopped repeated tool execution to prevent an infinite loop. Reason: ...`
3. Sets `StopReason = "aborted"`
4. Updates message in-place in `RecentMessages` AND `newMessages` via `replaceLast()`

### 5. Tool Call ID Generation (Non-Deterministic)
**Locations:**
- `tool_call_normalize.go:125` — `ensureToolCallID()`: `"tool_%d_%d", time.Now().UnixNano(), atomic.AddUint64(&toolCallSeq, 1)`
- `tool_tag_parser.go:133` — `buildToolCallMessage()`: `"tool_%d_%d", time.Now().UnixNano(), i`

**Cache Impact:** Even with identical LLM outputs and same correction logic, the serialized tool call IDs will differ on each run. Since tool call IDs appear in both the assistant message's `ToolCallContent.ID` and the corresponding `toolResult.ToolCallID`, both become non-deterministic.

### 6. Error Recovery

#### Tool Execution Errors (`tool_exec.go`)
**Cache Impact:** APPENDS toolResult messages — shifts prefix.

- `buildToolCallErrorMessage()`: Creates toolResult (role="toolResult", IsError=true) with error text
- `buildTruncationRecoveryMessage()`: Creates detailed format guidance for truncation errors with XML template examples
- All error results appended to `RecentMessages` and `newMessages`

#### LLM Errors (`llm_retry.go`)
**Cache Impact:** NO message injection. Retries re-call the LLM directly.
- `retryWithBackoff()`: Exponential backoff + jitter, up to `defaultRateLimitMaxRetries = 8` for rate limits
- On final failure, error propagates up — no messages added to history

#### Non-Success Stop Reason (`tool_guard.go`)
**Cache Impact:** IN-PLACE modification of last assistant message.
- `sanitizeMessageForNonSuccessStopReason()` appends a warning text block to the message content
- Updates message in-place in `RecentMessages`

### 7. Hooks (`hooks.go`)
**Cache Impact:** APPENDS messages — shifts prefix.

- `RunBeforeModel()`: Each `BeforeModelHook` can return extra messages that are appended to `RecentMessages`
- `RunAfterTool()`: Each `AfterToolHook` can modify a tool result in-place (chain-style)

### 8. Runtime Meta (`runtime_meta.go`)
**Cache Impact:** Modifies system-level content each turn (not message history, but changes what's sent to LLM).

- `injectRuntimeMeta()` computes telemetry (token usage, message count, CWD, etc.) and returns a YAML appendix
- This is injected into the system/user prompt area, not into `RecentMessages`
- Content changes every turn (token counts, message counts update)

---

## Key Patterns

### Message Injection Summary

| Mechanism | In-Place or Append | Role | Cache Impact |
|-----------|-------------------|------|-------------|
| `normalizeToolCall()` | In-place (assistant msg) | assistant | ✅ None (but non-deterministic ID) |
| `injectToolCallsFromTaggedText()` | In-place (assistant msg) | assistant | ✅ None (but non-deterministic ID) |
| `injectToolCallsFromThinking()` | In-place (assistant msg) | assistant | ✅ None (but non-deterministic ID) |
| Malformed recovery message | Append | user (Kind="tool_call_repair") | ❌ Shifts prefix |
| Loop guard soft feedback | Append | toolResult (IsError=true) | ❌ Shifts prefix |
| Loop guard hard abort | In-place (assistant msg) | assistant | ✅ None |
| Tool execution error | Append | toolResult (IsError=true) | ❌ Shifts prefix |
| Tool truncation recovery | Append | toolResult (IsError=true) | ❌ Shifts prefix |
| BeforeModel hooks | Append | variable | ❌ Shifts prefix |
| AfterTool hooks | In-place (toolResult) | toolResult | ✅ None |
| Non-success stop reason | In-place (assistant msg) | assistant | ✅ None |
| Tool output truncation | In-place (toolResult) | toolResult | ✅ None |
| Runtime meta | System-level | — | ❌ Changes every turn |
| LLM retry | No injection | — | ✅ None |

### Non-Determinism Sources

| Source | Location | Impact |
|--------|----------|--------|
| `time.Now().UnixNano()` in tool IDs | `tool_call_normalize.go:125` | Different ID each normalization |
| `time.Now().UnixNano()` in tag-parsed IDs | `tool_tag_parser.go:133` | Different ID each tag parse |
| `time.Now().UnixMilli()` in message timestamps | `llm_stream.go:223` | Different timestamp each message |

---

## Key Findings

1. **Two message-injection paths break prefix cache most often:**
   - **Malformed tool call recovery** (user message appended, Kind="tool_call_repair") — triggered when LLM emits tool-call markup that can't be parsed
   - **Loop guard feedback** (toolResult message appended with IsError=true) — triggered on repeated identical tool calls

2. **Tool call IDs are always non-deterministic** when generated by the framework (not the LLM). This means even "successful" in-place modifications produce different serialized content each turn, reducing the likelihood that Anthropic/other LLM caches will match.

3. **Tag-based tool call parsing is purely in-place** — it modifies the assistant message's Content blocks without adding history entries. This is the most cache-friendly correction path.

4. **The recovery flow for malformed calls is:**
   - LLM returns assistant message → tag parser tries in-place correction first
   - If that fails and message has no tool calls but looks like it tried → `maybeRecoverMalformedToolCall()` appends a user message
   - Max 2 recoveries before giving up

5. **Loop guard has two phases:** soft (append feedback, let LLM retry) → hard (strip tool calls in-place, abort). The soft phase is the one that shifts prefix.

6. **Tool execution errors always append** — even `buildTruncationRecoveryMessage()` with detailed XML templates. These are normal toolResult messages with IsError=true.

7. **Runtime meta changes every turn** — the YAML telemetry appendix sent to the LLM updates token counts, message counts, etc. each turn, which means even the "system" portion of the prompt can't be cached across turns.

---

## Gotchas

- **Tool call IDs include nanosecond timestamps:** Even if you fix everything else, `ensureToolCallID()` and `buildToolCallMessage()` generate IDs with `time.Now().UnixNano()`. Two sequential tool calls within the same nanosecond could theoretically collide, but more importantly, they're never reproducible.
- **Malformed recovery message is a USER role message:** This means it breaks the expected assistant→toolResult→assistant conversation pattern, inserting user→assistant at the end.
- **Loop guard feedback is appended as toolResult:** These have `ToolCallID` set to the blocked tool call's ID, maintaining the protocol pairing, but the feedback text varies by attempt number, making each unique.
- **`replaceLast()` panics on empty slice:** If `newMessages` is empty when called, the loop crashes. The code assumes at least one message exists at the call site.
- **BeforeModel hooks are fan-out, not chain:** Each hook receives the same input messages and returns extras that are all merged. Multiple hooks could produce conflicting messages.

---

## Relevance to Cache Hit Rate

To improve LLM prompt cache hit rates, focus on:

1. **Make tool call IDs deterministic** — use a hash of (name + args + turn counter) instead of `time.Now().UnixNano()`. This is the single biggest lever since every tool call goes through normalization.

2. **Avoid message injection on the happy path** — the tag parser already does this correctly (in-place). The malformed recovery path is the one to optimize: if tag parsing succeeds, no recovery message is needed.

3. **Make loop guard feedback deterministic** — the feedback message includes the attempt number and reason, but the tool call ID in the toolResult's ToolCallID field will be non-deterministic. Fixing #1 helps here too.

4. **Runtime meta could be moved to a fixed-size footer** — instead of changing every turn, use bucketed values that are less likely to change (e.g., token bands instead of exact counts).

5. **Timestamps on messages** — `time.Now().UnixMilli()` in `llm_stream.go:223` makes every assistant message unique. If the LLM provider uses exact byte matching for cache, this alone breaks it.

---

## Completeness Checklist

### Packages / Modules
- [x] `pkg/agent/tool_call_normalize.go` — tool call correction/normalization
- [x] `pkg/agent/tool_guard.go` — loop guard, malformed detection, recovery messages
- [x] `pkg/agent/tool_tag_parser.go` — XML tag parsing for tool calls from text/thinking
- [x] `pkg/agent/loop_state.go` — loopState, processToolResults(), handleMalformedToolCall(), replaceLast()
- [x] `pkg/agent/loop.go` — main loop config, constants, runInnerLoop()
- [x] `pkg/agent/tool_exec.go` — executeToolCalls(), error message building
- [x] `pkg/agent/tool_output.go` — output truncation (in-place)
- [x] `pkg/agent/llm_retry.go` — LLM retry with backoff (no message injection)
- [x] `pkg/agent/llm_stream.go` — stream processing, calls injectToolCalls on finalMessage
- [x] `pkg/agent/llm_stream_parse.go` — stream chunk state, tool call assembly
- [x] `pkg/agent/conversion.go` — ConvertMessagesToLLM()
- [x] `pkg/agent/hooks.go` — BeforeModel/AfterTool hooks
- [x] `pkg/agent/tool_metadata.go` — tool output summaries
- [x] `pkg/agent/runtime_meta.go` — runtime telemetry injection
- [x] `pkg/agent/executor.go` — concurrent tool executor (no message injection)
- [x] `pkg/context/message.go` — AgentMessage struct, constructors

### Key Behaviors
- [x] Malformed tool call detection (empty name, empty args+text, HTML markup, incomplete tags)
- [x] Malformed tool call recovery (user message append, max 2 recoveries)
- [x] Tool call normalization (name cleanup, alias mapping, args unwrapping, ID generation)
- [x] XML tag parsing from text content (in-place assistant message modification)
- [x] XML tag parsing from thinking content (in-place assistant message modification)
- [x] Loop guard soft feedback (append toolResult with actionable text)
- [x] Loop guard hard abort (in-place strip tool calls + abort message)
- [x] Tool execution error handling (append toolResult with IsError=true)
- [x] Tool truncation recovery (append toolResult with detailed format guidance)
- [x] LLM error retry (no message injection, just re-calls LLM)
- [x] Non-success stop reason handling (in-place message modification)
- [x] Tool output truncation (in-place content modification)
- [x] BeforeModel hooks (append messages)
- [x] AfterTool hooks (in-place result modification)
- [x] Runtime meta injection (system-level, changes every turn)

### Cross-cutting Concerns
- [x] Non-deterministic ID generation (time.Now().UnixNano())
- [x] Non-deterministic timestamps (time.Now().UnixMilli())
- [x] Message visibility filtering (IsAgentVisible() in ConvertMessagesToLLM)
- [x] Error handling patterns (toolResult with IsError vs. error propagation)
- [x] Recovery limits (malformedRecs, emptyRetries, loopGuard feedback count)
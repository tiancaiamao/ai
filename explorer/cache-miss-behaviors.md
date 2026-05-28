# Explorer: Cache-Miss Behaviors — LLM Prefix Cache Analysis

## Overview
Analysis of all behaviors in the codebase that would cause **LLM prefix cache misses** when using providers like DeepSeek that require exact byte-prefix matching for automatic prefix caching.

## What is Prefix Caching?
Providers like DeepSeek cache the exact byte prefix of requests (system prompt + messages). If the first N bytes of a request match a cached prefix, the provider reuses the cached KV-state. Any change — even a single byte — in the prefix invalidates the entire cached prefix from that point forward.

## Critical Insight: How Messages are Assembled Per Turn

The request assembly pipeline (in `streamAssistantResponse`, `pkg/agent/llm_stream.go:30-70`):

```
1. selectedMessages = agentCtx.RecentMessages        (all history)
2. llmMessages = ConvertMessagesToLLM(selectedMessages) (to LLM format)
3. systemPrompt = agentCtx.SystemPrompt               (from createBaseContext)
4. systemPrompt += ThinkingInstruction(...)            (appended)
5. runtimeAppendix = injectRuntimeMeta(agentCtx, config) (runtime YAML + llm_context)
6. runtimeAppendix inserted BEFORE last user message  (position shift!)
7. llmTools = ConvertToolsToLLM(agentCtx.Tools)       (tool schemas)
8. API request = { model, messages: [system]+llmMessages, stream: true, tools }
```

**The system prompt is sent as the first message with role="system", followed by the message array.**

---

## Finding 1: RUNTIME META INJECTION (HIGH IMPACT) — Intentional

**File:** `pkg/agent/llm_stream.go:30-70`, `pkg/agent/runtime_meta.go`

**Behavior:** `injectRuntimeMeta()` builds a `runtime_state` YAML block containing telemetry (token usage, tool pressure, message counts, working directory) and injects it as a **USER message BEFORE the last user message** via `insertBeforeLastUserMessage()`.

**What changes per turn:**
- `tokens_band` — bucketed token usage (changes as conversation grows)
- `tokens_used_approx` — normalized approximation of total tokens used
- `messages_in_history_bucket` — bucketed message count
- `current_workdir` — can change via `change_workspace` tool
- `stale_tool_outputs` — count of stale outputs
- `large_tool_outputs` — count of large outputs
- `largest_tool_output_bucket` — size bucket

**Heartbeat mechanism (partial mitigation):**
`updateRuntimeMetaSnapshot()` (line 334) only refreshes the snapshot when:
- The snapshot is empty (first turn)
- The token band changed
- The heartbeat counter reaches threshold (default: 6 turns via `defaultRuntimeMetaHeartbeatTurns`)

**Cache impact:**
- **Turn 1:** `[system] [runtime_state_1] [user_1]`
- **Turn 2:** `[system] [user_1] [assistant_1] [runtime_state_2] [user_2]`

The runtime_state message is **positioned differently** each turn (it's always inserted before the LAST user message). This means the prefix diverges immediately after the system prompt because the first message after system is different each turn (runtime_state vs user_1 vs ...).

**Impact: HIGH** — Breaks prefix cache on EVERY turn due to positional shifting of the runtime_state message. Even when the snapshot content is identical (within heartbeat window), the insertion position changes as messages accumulate.

---

## Finding 2: LLM CONTEXT CONTENT INJECTION (MEDIUM-HIGH IMPACT) — Intentional

**File:** `pkg/agent/llm_stream.go:302`, `pkg/agent/runtime_meta.go:17-24`, `pkg/tools/context_mgmt/update_llm_context.go`

**Behavior:** `agentCtx.LLMContext` (maintained by the LLM via `update_llm_context` tool) is injected as `<llm_context>...</llm_context>` block in the runtime appendix (same user message as runtime_state).

**What changes:** LLM calls `update_llm_context` tool to update this content. It changes whenever the LLM decides to update it. Changes are unpredictable.

**Cache impact:** Since it's bundled with the runtime_state message (Finding 1), any change to LLMContext invalidates the prefix from that message forward. But the positional issue dominates (Finding 1).

**Impact: MEDIUM-HIGH** — Changes are less frequent than runtime_state, but when they occur, they compound with the positional issue.

---

## Finding 3: THINKING INSTRUCTION APPENDED TO SYSTEM PROMPT (MEDIUM IMPACT) — Intentional

**File:** `pkg/agent/llm_stream.go:40-47`, `pkg/prompt/builder.go:318-340`

**Behavior:**
```go
systemPrompt := agentCtx.SystemPrompt
if instruction := prompt.ThinkingInstruction(thinkingLevel); instruction != "" {
    systemPrompt = systemPrompt + "\n\n" + instruction
}
```

**What changes:** The thinking level can be changed mid-session via `set thinking-level` command (registered in `rpc_config_handlers.go:588`). Default is "high".

**Cache impact:** If the user changes thinking level mid-session, the system prompt changes, invalidating the ENTIRE prefix cache. Between turns at the same thinking level, this is stable.

**Impact: MEDIUM** — Stable within a session unless explicitly changed. When changed, complete cache invalidation.

---

## Finding 4: TOOL DEFINITIONS (LOW IMPACT — STATIC) — Intentional

**File:** `pkg/agent/conversion.go`, `pkg/tools/*.go`

**Behavior:** `ConvertToolsToLLM()` converts tools using `tool.Name()`, `tool.Description()`, `tool.Parameters()`. All tool definitions return **hardcoded static strings** — no dynamic content.

**Tools registered:** `bash`, `read`, `edit`, `write`, `grep`, `change_workspace`, `find_skill`, `compact`, `truncate_messages`, `update_llm_context`, `no_action`.

**Cache impact:** Tool definitions are stable across turns within a session. They change only if tools are added/removed (which doesn't happen during normal operation).

**Impact: LOW** — Static within a session.

---

## Finding 5: SKILL FORMATTING WITH PROGRESSIVE DISCLOSURE (LOW-MEDIUM IMPACT) — Accidental

**File:** `pkg/skill/formatter.go`, `pkg/prompt/builder.go:209`

**Behavior:** `FormatForPrompt(skills, skillStats)` uses `SkillStatsFile` for ranking skills by usage. Only the top-N ranked skills are shown; others are omitted with "N additional skills omitted for brevity."

**What changes:** As `skillStats` are updated (via `RecordUsage()` when skills are used), the ranking/order of skills in the system prompt may change, and different skills may be included/excluded.

**Cache impact:** Since skills are embedded in the system prompt (via `%SKILLS%` placeholder), any change to the skill ranking/order changes the system prompt bytes, invalidating the entire prefix cache.

**Impact: LOW-MEDIUM** — Skill stats change only when skills are actually used (infrequent). When they change, complete cache invalidation because it's in the system prompt.

---

## Finding 6: COMPACT/COMPACTION REBUILDS ENTIRE MESSAGE ARRAY (HIGH IMPACT) — Intentional

**File:** `pkg/compact/compact.go:1040-1065`, `pkg/context/message.go:142-153`

**Behavior:** When compaction triggers:
```go
newRecentMessages := []agentctx.AgentMessage{
    agentctx.NewCompactionSummaryMessage(summary),  // "[Previous conversation summary]\n\n{summary}"
}
newRecentMessages = append(newRecentMessages, recentMessages...)
ctx.RecentMessages = newRecentMessages
```

The compaction summary message gets a fresh `time.Now().UnixMilli()` timestamp each time.

**What changes:** The ENTIRE message array is rebuilt:
- All old messages replaced with a single summary message
- Summary content is different every time (LLM-generated)
- New timestamp on the summary message
- All subsequent messages are different from the pre-compaction state

**Cache impact:** Complete prefix invalidation after compaction. The system prompt is followed by an entirely new first message. Nothing from the previous cache is reusable.

**Impact: HIGH** — By design, compaction fundamentally changes the prefix. This is expected behavior.

---

## Finding 7: MALFORMED TOOL CALL RECOVERY (LOW IMPACT) — Intentional

**File:** `pkg/agent/tool_guard.go:334-349`

**Behavior:** When the LLM emits a malformed tool call, a recovery message is injected:
```go
text := fmt.Sprintf(
    "[agentctx.Tool-call recovery, attempt %d] Your previous response...",
    attempt, truncateLine(cleanReason, 220))
```

**What changes:** The `attempt` counter increments (1, 2, ... up to `defaultMalformedToolCallRecoveries = 2`). The `reason` text varies based on the parse failure.

**Cache impact:** These messages are inserted into the message array at the point where the error occurred. They change the prefix from that point forward. However, malformed tool calls are rare in practice.

**Impact: LOW** — Rare occurrence. When it happens, prefix breaks at the message insertion point.

---

## Finding 8: SESSION RESUME REBUILDS PROMPT AND MESSAGES (HIGH IMPACT) — Intentional

**File:** `pkg/session/lazy.go`, `pkg/context/reconstruction.go`, `cmd/ai/rpc_session_handlers.go:21`, `cmd/ai/rpc_app.go:232-234`

**Behavior:** When a session is resumed:
1. `createBaseContext()` is called, which calls `buildSystemPrompt()` fresh
2. `buildSystemPrompt()` creates a **new** `prompt.Builder` each call
3. Messages are reconstructed from journal entries via `ReconstructSnapshotWithCheckpoint()`
4. Compaction summary is restored as `[Previous conversation summary]` message
5. `SetSkillStats(app.skillStats)` uses current stats (which may differ from when session was last active)

**Cache impact:** Complete cache invalidation on resume — the system prompt is rebuilt, skill rankings may differ, message timestamps may differ.

**Impact: HIGH** — Expected behavior on session resume. Not a concern for within-session caching.

---

## Finding 9: SYSTEM PROMPT REBUILD PER createBaseContext() (MEDIUM IMPACT) — Accidental

**File:** `cmd/ai/rpc_app.go:178-196`

**Behavior:**
```go
app.buildSystemPrompt = func(currentSess *session.Session) string {
    promptBuilder := prompt.NewBuilderWithWorkspace("", app.ws)
    promptBuilder.SetTools(app.registry.All()).SetSkills(app.skillResult.Skills).SetSkillStats(app.skillStats)
    // ... reads bootstrap files from CWD
    return promptBuilder.Build()
}
```

`buildSystemPrompt()` creates a **new builder** every call, reads bootstrap files from disk (`TOOLS.md`, `IDENTITY.md`, `AGENTS.md` from CWD), and uses current `app.skillStats`.

**When called:**
- On `setSession()` (session switches)
- On `createBaseContext()` (which is called on session switches and initial setup)
- **NOT called per-turn** during normal operation

**Cache impact:** Within a single session, the system prompt is stable (built once). Between sessions or after `/resume`, it's rebuilt. The `buildProjectContext()` reads files from disk, so if the user edits `AGENTS.md` between turns, the system prompt would change.

**Impact: MEDIUM** — Stable within a session, but `createBaseContext()` is called on every session switch and could theoretically be called more often.

---

## Finding 10: PROJECT_CONTEXT WORKSPACE NOTES (LOW IMPACT) — Intentional

**File:** `pkg/prompt/builder.go:231-260`

**Behavior:** `%PROJECT_CONTEXT%` includes bootstrap files (`TOOLS.md`, `IDENTITY.md`, `AGENTS.md`) read from CWD via `os.ReadFile()`.

**What changes:** File content changes if:
- User edits these files during a session
- User changes directory (via `change_workspace` tool)

**Cache impact:** Since this is embedded in the system prompt, any change invalidates the entire prefix. However, these files rarely change during a session.

**Impact: LOW** — Rarely changes within a session. When it does, complete cache invalidation.

---

## Additional Findings

### `ensureToolCallID()` — Per-Call ID Generation
**File:** `pkg/agent/tool_call_normalize.go:125`
```go
return fmt.Sprintf("tool_%d_%d", time.Now().UnixNano(), seq)
```
Uses `time.Now().UnixNano()` + atomic counter for tool calls missing IDs. This only affects tool call IDs in the message content (not the prefix), since these appear in tool results that come later in the array. **Impact on prefix: NEGLIGIBLE** — tool results appear after the prefix.

### `truncate_messages` Tool — Message Mutation
**File:** `pkg/tools/context_mgmt/truncate_messages.go`
Replaces message content with head/tail summary. Changes message bytes in-place. **Impact: MEDIUM** — changes existing messages in the array, which can break the prefix from the truncation point forward.

### No Few-Shot Examples
The codebase does **not** use few-shot examples in the system prompt. The `%SKILLS%` section includes skill descriptions but not example interactions.

### API Request Structure — Static
**File:** `pkg/llm/client.go:82-88`
```go
reqBody := map[string]any{
    "model":    model.ID,
    "messages": messages,
    "stream":   true,
}
if len(llmCtx.Tools) > 0 {
    reqBody["tools"] = llmCtx.Tools
    reqBody["tool_choice"] = "auto"
}
```
No `temperature`, `max_tokens`, `seed`, `top_p`, or other variable parameters. **Completely static structure.** No per-turn variation in request parameters.

---

## Summary Table

| # | Behavior | Impact | Type | File | Changes Per-Turn? |
|---|----------|--------|------|------|--------------------|
| 1 | Runtime meta injection (runtime_state YAML) | **HIGH** | Intentional | `llm_stream.go` | Yes — positional shift + content |
| 2 | LLM Context (`<llm_context>` block) | **MEDIUM-HIGH** | Intentional | `runtime_meta.go` | When LLM calls update tool |
| 3 | Thinking instruction appended to system prompt | **MEDIUM** | Intentional | `llm_stream.go` | Only when thinking level changes |
| 4 | Tool definitions | **LOW** | Intentional | `conversion.go` | No — static |
| 5 | Skill formatting with progressive disclosure | **LOW-MEDIUM** | Accidental | `formatter.go` | When skill stats update |
| 6 | Compaction rebuilds message array | **HIGH** | Intentional | `compact.go` | On compaction events |
| 7 | Malformed tool call recovery | **LOW** | Intentional | `tool_guard.go` | On malformed calls (rare) |
| 8 | Session resume reconstruction | **HIGH** | Intentional | `lazy.go`, `rpc_app.go` | On resume (not per-turn) |
| 9 | System prompt rebuild per createBaseContext() | **MEDIUM** | Accidental | `rpc_app.go` | On session switches |
| 10 | Project context (bootstrap files) | **LOW** | Intentional | `builder.go` | When files edited |

---

## Root Cause Analysis: Why Prefix Caching Fails

### The #1 Problem: Runtime State Message Position

The fundamental issue is **Finding 1**: the runtime_state YAML is injected as a user message **before the last user message** on every turn. This means:

```
Turn 1: [SYSTEM] [runtime_state_1] [user_1]
                                         → LLM response

Turn 2: [SYSTEM] [user_1] [assistant_1] [runtime_state_2] [user_2]
                                            → LLM response

Turn 3: [SYSTEM] [user_1] [assistant_1] [user_2] [assistant_2] [runtime_state_3] [user_3]
                                                                  → LLM response
```

The runtime_state message is **never in the same position twice** — it's always right before the last user message, which shifts forward as the conversation grows. This means the first message after the system prompt is DIFFERENT every turn (runtime_state_1, user_1, user_1 respectively), so the prefix cache breaks after the system prompt bytes.

Even if the runtime_state content is unchanged (within the 6-turn heartbeat), the POSITION changes, so the cache still misses.

### Secondary Issues

- **Finding 6 (compaction):** When compaction fires, the entire message prefix changes. This is unavoidable by design.
- **Finding 5 (skill stats):** Changes to skill rankings modify the system prompt itself, which invalidates from byte 0.

### What Would Fix It

For effective prefix caching, the runtime_state should be moved to one of:
1. **End of system prompt** — append as system prompt content (changes system prompt, but at least it's after all static content)
2. **After the last user message** — as a trailing user message (position still shifts, but doesn't break the prefix of older messages)
3. **Separate message after system** — if always in position 1, the prefix at least stays stable from system through message N

---

## Completeness Checklist

### Packages / Modules
- [x] `pkg/agent/` — agent loop, LLM streaming, runtime meta injection, tool guards
- [x] `pkg/context/` — message types, context state, reconstruction, checkpoints
- [x] `pkg/compact/` — compaction logic, context management
- [x] `pkg/prompt/` — system prompt builder, template, thinking instructions
- [x] `pkg/llm/` — LLM client, API request structure, types
- [x] `pkg/tools/` — tool registry, tool definitions
- [x] `pkg/tools/context_mgmt/` — update_llm_context, truncate_messages tools
- [x] `pkg/skill/` — skill formatting, stats
- [x] `pkg/session/` — session loading, lazy loading, compaction events
- [x] `pkg/agentconfig/` — agent config, system prompt resolution
- [x] `cmd/ai/` — RPC handlers, app initialization, session management

### Public API Surface
- [x] RPC command: `prompt` (main entry point for user messages)
- [x] RPC command: `steer` (follow-up messages)
- [x] RPC command: `session` (session state)
- [x] RPC command: `resume` (session resume)
- [x] RPC command: `rewind` (conversation rewind)
- [x] RPC command: `fork` (conversation fork)
- [x] RPC command: `set` (config changes including thinking-level)

### Key Behaviors
- [x] Multi-turn tool use loop (`RunLoop` in `loop.go`)
- [x] Runtime meta injection per turn (`injectRuntimeMeta`)
- [x] Runtime meta heartbeat/snapshot caching (`updateRuntimeMetaSnapshot`)
- [x] Thinking instruction appended to system prompt
- [x] Progressive skill disclosure based on stats
- [x] Major compaction (summary + message rebuild)
- [x] Mini compaction (tool output truncation)
- [x] Message truncation via `truncate_messages` tool
- [x] Malformed tool call recovery messages
- [x] Session resume with journal reconstruction
- [x] System prompt rebuild on session switches
- [x] Bootstrap file loading for project context
- [x] LLM context update via `update_llm_context` tool
- [x] Tool call ID normalization with `time.Now().UnixNano()`

### Cross-cutting Concerns
- [x] Error handling pattern (recovery messages as user messages)
- [x] Session persistence (journal-based, append-only)
- [x] Checkpoint system for crash recovery
- [x] Workspace CWD management (dynamic, affects runtime_state)
- [x] Tool output size management (truncation, compaction)
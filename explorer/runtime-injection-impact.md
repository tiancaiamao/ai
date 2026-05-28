# Explorer: Runtime Message Injection Impact on LLM Prefix Cache

## Overview
This document catalogs every point where the agent runtime injects or modifies messages/content that becomes part of the LLM API call, with analysis of impact on LLM prefix cache hit rate.

## Cache Impact Model
LLM prefix cache works by matching sequential tokens from the start of a request. Any change to **early content** (system prompt, first messages) invalidates the entire cached prefix. Changes to **later content** (messages near the end) preserve the earlier prefix.

**Key principle:** System prompt is the most cache-sensitive (position 0). Later message insertions are progressively less damaging.

---

## Injection Points (ordered by cache sensitivity: most → least)

### 1. System Prompt — Dynamic Content (`pkg/prompt/builder.go` + `pkg/agent/llm_stream.go`)

**Cache Impact: 🔴 CRITICAL — Any change invalidates entire prefix cache**

The system prompt is built once per `createBaseContext()` call (at startup, session switch, or fork). It is sent as the first element of every LLM call.

#### 1a. Template Selection (`TemplateForRole`)
- **File:** `pkg/prompt/builder.go:396`
- **Trigger:** Agent startup, configured by `--role` flag (coder/orchestrator/validator)
- **Content:** Static embedded templates: `prompt.md`, `orchestrator.md`, `validator.md`
- **Stability:** ✅ Stable — template does not change within a session

#### 1b. Bootstrap Files (Project Context)
- **File:** `pkg/prompt/builder.go:283-297` (`buildProjectContext`)
- **Trigger:** Each `Build()` call
- **Content:** Reads files from CWD: `TOOLS.md`, `IDENTITY.md`, `AGENTS.md` (or `.ai/<file>`)
- **Position:** In `%PROJECT_CONTEXT%` placeholder within template
- **Stability:** ⚠️ Changes if files on disk change. Currently read at Build() time but system prompt is cached per-session. Not re-read within a session unless `createBaseContext()` is called again.

#### 1c. Skill Index
- **File:** `pkg/prompt/builder.go:267-273` (`SetSkills`, `SetSkillStats`)
- **Trigger:** Each `Build()` call from `createBaseContext()`
- **Content:** `skill.FormatForPrompt(skills, skillStats)` — formatted skill list with progressive disclosure stats
- **Position:** In `%SKILLS%` placeholder
- **Stability:** ⚠️ Skills and skill stats are loaded at startup. Not refreshed mid-session.

#### 1d. Workspace Section
- **File:** `pkg/prompt/builder.go:258-265`
- **Trigger:** Each `Build()` call
- **Content:** Static text with optional `workspaceNotes`
- **Stability:** ✅ Stable — workspaceNotes is set once

#### 1e. Tool Definitions
- **File:** `pkg/agent/conversion.go:74-100` (`ConvertToolsToLLM`)
- **Trigger:** EVERY LLM call in `streamAssistantResponse` (line ~65: `llmTools := ConvertToolsToLLM(ctx, agentCtx.Tools)`)
- **Content:** JSON schema of all registered tools
- **Position:** `llm.LLMContext.Tools` — sent alongside system prompt
- **Stability:** ⚠️ Tools come from `agentCtx.Tools` which is set at `createBaseContext()`. Not dynamically modified mid-session. No MCP tool loading/unloading exists in this codebase.

#### 1f. ThinkingInstruction (Appended to System Prompt)
- **File:** `pkg/agent/llm_stream.go:39-43`
- **Trigger:** EVERY LLM call
- **Content:** Appended to system prompt string based on `config.ThinkingLevel`
  - Levels: off, minimal, low, medium, high, xhigh
  - Example: `"Thinking level is high. Use thorough reasoning where needed."`
- **Position:** Concatenated after `agentCtx.SystemPrompt` before sending to LLM
- **Code:**
  ```go
  systemPrompt := agentCtx.SystemPrompt
  if instruction := prompt.ThinkingInstruction(thinkingLevel); instruction != "" {
      systemPrompt += "\n\n" + instruction
  }
  ```
- **Stability:** ✅ Stable within a session — ThinkingLevel doesn't change mid-session (set at startup via config). If `SetThinkingLevel()` is called via RPC slash command, the change takes effect on the NEXT LLM call, changing the system prompt suffix and invalidating prefix cache.
- **Cache Impact:** Change invalidates the ENTIRE system prompt prefix cache.

---

### 2. Runtime Meta Injection (`pkg/agent/runtime_meta.go` + `pkg/agent/llm_stream.go`)

**Cache Impact: 🟠 HIGH — Inserts before last user message, breaking suffix cache**

- **File:** `pkg/agent/llm_stream.go:55-72` (orchestration), `pkg/agent/runtime_meta.go:18-113` (content)
- **Trigger:** EVERY LLM call via `streamAssistantResponse`
- **Content:** YAML block containing:
  - `runtime_state` telemetry: `tokens_band`, `tokens_used_approx`, `tokens_max`, `messages_in_history_bucket`, `llm_context_size_bucket`
  - `<llm_context>` block (if `agentCtx.LLMContext` is non-empty)
  - Reminder text: `"Remember: runtime_state is telemetry, not user intent..."`
- **Position:** Inserted as a `user` role message BEFORE the last user message via `insertBeforeLastUserMessage()`
- **Role:** `user` (synthetic)
- **Update Schedule:** Band-based — snapshot only regenerated when token usage band changes (not every turn). The `RuntimeMetaTurns` counter resets each time the band shifts.
- **Code:**
  ```go
  runtimeAppendix := injectRuntimeMeta(agentCtx, config)
  if runtimeAppendix != "" {
      runtimeMsg := llm.LLMMessage{Role: "user", Content: runtimeAppendix}
      llmMessages = insertBeforeLastUserMessage(llmMessages, runtimeMsg)
  }
  ```
- **Cache Impact:** Since this message is inserted before the last user message, it changes the message sequence at that position. However, because the snapshot only changes when the band shifts, the content is often identical across turns within the same band, meaning the prefix up to and including this message may still hit cache on subsequent turns.

---

### 3. Hook-Injected Messages (`pkg/agent/hooks.go`)

**Cache Impact: 🟠 HIGH — Injects into RecentMessages before LLM call**

- **File:** `pkg/agent/hooks.go:63-79` (`RunBeforeModel`)
- **Trigger:** Every LLM call via `prepareLLMMessages` → `s.config.Hooks.RunBeforeModel()`
- **Content:** Arbitrary — depends on hook implementation
- **Role:** Can be any role (hooks return `[]AgentMessage`)
- **Mechanism:** Fan-out — each hook receives same input messages; outputs are merged by appending to `RecentMessages`
- **Stability:** Depends entirely on hook implementation. In tests, hooks inject `Role: "framework"` messages.
- **Cache Impact:** Any injected messages that differ from the previous turn will invalidate the prefix cache from the injection point forward.

#### AfterTool Hooks
- **File:** `pkg/agent/hooks.go:86-99` (`RunAfterTool`)
- **Trigger:** After every tool execution, before result is appended
- **Content:** Can modify any tool result message
- **Mechanism:** Chain — output of each hook becomes input of next
- **Cache Impact:** Modified tool results change message content, potentially breaking cache.

---

### 4. Compaction Summary Messages (`pkg/session/entries.go` + `pkg/context/message.go`)

**Cache Impact: 🔴 CRITICAL — Replaces entire conversation history**

- **File:** `pkg/session/entries.go:23-24` (constants), `pkg/context/message.go:140-150` (constructor)
- **Trigger:** Compaction event (manual `/compact`, auto-compaction at threshold, context limit recovery)
- **Content:** Role=`user`, Kind=`compactionSummary`
  ```
  [Previous conversation summary]

  The conversation history before this point was compacted into the following summary:
  <summary>
  ... summary text ...
  </summary>
  ```
- **Position:** Replaces ALL prior messages in `RecentMessages` — becomes the first message after system prompt
- **Message Constructor:**
  ```go
  func NewCompactionSummaryMessage(summary string) AgentMessage {
      return AgentMessage{
          Role: "user",
          Content: []ContentBlock{TextContent{Type: "text", Text: fmt.Sprintf("[Previous conversation summary]\n\n%s", summary)}},
          Metadata: &MessageMetadata{Kind: "compactionSummary"},
      }
  }
  ```
- **Session Replay Constants:**
  ```go
  CompactionSummaryPrefix = "The conversation history before this point was compacted into the following summary:\n\n<summary>\n"
  CompactionSummarySuffix = "\n</summary>"
  ```
- **Cache Impact:** COMPLETE INVALIDATION. All previously cached prefix is lost because the entire message history is replaced with new content.

#### Branch Summary Messages
- **Trigger:** Session branch resume (`handleResume`, `handleRewind`, `handleFork`)
- **Content:** `BranchSummaryPrefix + summary + BranchSummarySuffix`
  ```go
  BranchSummaryPrefix = "The following is a summary of a branch that this conversation came back from:\n\n<summary>\n"
  BranchSummarySuffix = "\n</summary>"
  ```
- **Position:** Injected as `user` message in conversation history during session replay
- **Cache Impact:** COMPLETE INVALIDATION — new context has no prefix overlap with previous session.

---

### 5. Context Management Truncation (`pkg/compact/context_management.go`)

**Cache Impact: 🟡 MEDIUM — Modifies message content in-place**

- **Trigger:** Periodic background LLM call when token usage exceeds thresholds (20%/33%/50%)
- **Content:** Truncates tool outputs using `TruncateWithHeadTail` — replaces original content with head+tail preview
- **Mechanism:** 
  1. Context manager makes independent LLM call with `truncate_messages`/`update_llm_context` tools
  2. Truncation modifies `agentCtx.RecentMessages` in-place
  3. Can also update `agentCtx.LLMContext` via `update_llm_context` tool
- **Visibility:** Truncated messages keep their original role/position but have `Truncated: true` flag
- **Cache Impact:** Modified messages change the prefix. Since truncation only affects older messages (not recent ones), the impact depends on which messages are truncated and their position in the conversation.

---

### 6. Tool Loop Guard Feedback (`pkg/agent/tool_guard.go`)

**Cache Impact: 🟡 MEDIUM — Injects additional tool_result messages**

- **File:** `pkg/agent/tool_guard.go:124-175` (`buildLoopGuardToolResults`)
- **Trigger:** When LLM emits repeated identical tool call signatures (same name + same arguments hash)
- **Content:** ToolResult messages with actionable feedback:
  ```
  [Loop guard] You have called {tool_name} with identical arguments {N} consecutive times.
  This suggests the call is not making progress. Consider:
  - Using different arguments or a different approach
  - Checking if the tool result contains an error you need to address
  - Moving on to a different step in your plan
  ```
- **Role:** `toolResult` (appears as `tool` role to LLM after conversion)
- **Position:** Appended after the assistant's tool call message
- **Escalation:** After `maxFeedbackAttempts` (default: from `defaultLoopGuardMaxFeedback`), escalates to hard abort
- **Cache Impact:** Additional messages extend the conversation, potentially breaking suffix cache. The feedback content is deterministic for the same repetition count, so repeated identical loops may see partial cache hits.

---

### 7. Malformed Tool Call Recovery (`pkg/agent/tool_guard.go`)

**Cache Impact: 🟡 MEDIUM — Injects user message mid-conversation**

- **File:** `pkg/agent/tool_guard.go:246-344` (`maybeRecoverMalformedToolCall`, `buildMalformedToolCallRecoveryMessage`)
- **Trigger:** When LLM emits tool call XML/function-call markup that fails to parse into valid ToolCallContent
- **Content:** Role=`user`, Kind=`tool_call_repair`
  ```
  [agentctx.Tool-call recovery, attempt N] Your previous response attempted a tool
  invocation but the tool call format was invalid (reason). Re-emit the intended call
  using valid tool/function-call syntax only...
  ```
- **Visibility:** Agent-visible (`true`), User-hidden (`false`) — via `.WithVisibility(true, false)`
- **Position:** Appended to `RecentMessages` after the malformed assistant message
- **Max recoveries:** `defaultMalformedToolCallRecoveries = 2`
- **Code:**
  ```go
  func buildMalformedToolCallRecoveryMessage(reason string, attempt int) AgentMessage {
      text := fmt.Sprintf("[agentctx.Tool-call recovery, attempt %d] ...", attempt, ...)
      return NewUserMessage(text).WithVisibility(true, false).WithKind("tool_call_repair")
  }
  ```
- **Cache Impact:** Inserts a user message that changes the alternating user/assistant pattern. This breaks cache from the insertion point forward.

---

### 8. Session Resume/Context Restoration (`cmd/ai/rpc_app.go` + `cmd/ai/rpc_session_handlers.go`)

**Cache Impact: 🔴 CRITICAL — Complete context replacement**

- **Trigger:** Session switch (`handleResume`), fork (`handleFork`), rewind (`handleRewind`)
- **Mechanism:**
  1. `createBaseContext()` rebuilds the entire `AgentContext` from session data
  2. `restoreLLMContextFromCompaction()` restores `llm_context.txt` from latest compaction summary
  3. Session replay reconstructs `RecentMessages` from session entries, including:
     - Compaction summary entries → `compactionSummaryMessage()`
     - Branch summary entries → `branchSummaryMessage()`
     - Compact events → applies truncation to tool outputs
- **Code (`createBaseContext`):**
  ```go
  app.createBaseContext = func() *agentctx.AgentContext {
      ctx := agentctx.NewAgentContext(app.systemPrompt())
      ctx.Tools = app.registry.All()
      // ... checkpoint restoration, LLM context, etc.
      return ctx
  }
  ```
- **Cache Impact:** COMPLETE INVALIDATION. New session = new system prompt + new messages = zero prefix overlap.

---

### 9. Tool Output Truncation at Execution Time (`pkg/agent/tool_output.go`)

**Cache Impact: 🟡 MEDIUM — Modifies tool result content**

- **Trigger:** Every tool execution (when output exceeds `MaxChars`, default 10,000)
- **Content:** Truncates tool output using `truncate.TruncateWithHeadTail`
- **Position:** The tool result message itself (role=toolResult → tool)
- **Special handling:** Error-containing outputs get extra head allocation (70/30 split vs default 50/50)
- **Cache Impact:** Truncated outputs differ from full outputs. However, since truncation is deterministic (same input → same truncated output), repeated identical tool calls will produce the same truncated result, preserving cache.

---

### 10. `update_llm_context` Tool (`pkg/tools/context_mgmt/update_llm_context.go`)

**Cache Impact: 🟡 MEDIUM — Changes LLMContext content**

- **Trigger:** LLM calls `update_llm_context` tool
- **Content:** Updates `agentCtx.LLMContext` string — this content is then injected into the runtime_meta message on the NEXT LLM call
- **Position:** Inside the `<llm_context>` block of the runtime state injection (injection #2)
- **Code:**
  ```go
  t.agentCtx.LLMContext = llmContext  // updates in-memory snapshot
  ```
- **Cache Impact:** Changes the runtime_state injection content. Since runtime_state uses band-based updates, the change may not appear immediately. When it does appear (on next band shift), it modifies the runtime meta user message, breaking cache from that point.

---

## Summary: Injection Frequency Table

| # | Injection Point | Trigger Frequency | Position | Cache Damage |
|---|---|---|---|---|
| 1a | Template Selection | Per session startup | System prompt | — (stable) |
| 1b | Bootstrap Files | Per `createBaseContext()` | System prompt | — (stable within session) |
| 1c | Skill Index | Per `createBaseContext()` | System prompt | — (stable within session) |
| 1d | Workspace Section | Per `Build()` | System prompt | — (stable) |
| 1e | Tool Definitions | Every LLM call | System prompt (tools field) | — (stable within session) |
| 1f | ThinkingInstruction | Every LLM call | System prompt suffix | ⚠️ Changes if ThinkingLevel changes |
| 2 | Runtime Meta (runtime_state + llm_context) | Every LLM call | Before last user message | 🟠 Band-based updates — stable within band |
| 3 | Compaction Summary | On compaction | Replaces all messages | 🔴 Complete invalidation |
| 3b | Branch Summary | On session resume | In message history | 🔴 Complete invalidation |
| 4a | BeforeModel Hooks | Every LLM call | Appended to RecentMessages | 🟠 Depends on hook output |
| 4b | AfterTool Hooks | Every tool execution | Modifies tool result | 🟠 Depends on hook output |
| 5 | Context Management Truncation | Periodic (20%/33%/50% thresholds) | In-place message modification | 🟡 Older messages only |
| 6 | Loop Guard Feedback | On repeated tool calls | After assistant message | 🟡 Extends conversation |
| 7 | Malformed Tool Call Recovery | On parse failure | After assistant message | 🟡 Inserts user message |
| 8 | Session Resume | On session switch | Complete replacement | 🔴 Complete invalidation |
| 9 | Tool Output Truncation | Every large tool result | Tool result message | 🟡 Deterministic |
| 10 | `update_llm_context` | LLM-initiated | Affects runtime_state | 🟡 Delayed (band-based) |

---

## Key Findings

1. **System prompt is mostly stable** within a session. The only dynamic element appended per-call is `ThinkingInstruction`, but this is also stable unless `set_thinking_level` is called via RPC.

2. **Runtime state is the most frequent changer**, but its band-based update schedule limits cache damage. Within a band (e.g., tokens 20-40%), the snapshot content is identical across turns, so the prefix including the runtime state message can be cached.

3. **The `insertBeforeLastUserMessage` strategy is well-designed** for cache: it means runtime state changes don't affect the position of messages before the insertion point. The prefix up to (but not including) the runtime state message remains cacheable.

4. **Compaction is the nuclear option for cache** — it replaces everything. After compaction, there is zero prefix overlap with the pre-compaction conversation.

5. **No MCP dynamic tool injection exists** in this codebase. The tool list is set at `createBaseContext()` time and remains static within a session.

6. **Tool definitions are sent with every LLM call** (`ConvertToolsToLLM` in `streamAssistantResponse`), but since the tool list doesn't change within a session, this is cache-stable.

7. **Hook-injected messages are the wildcard** — `BeforeModelHook` can inject arbitrary messages that append to `RecentMessages`. The cache impact depends entirely on the hook implementation.

8. **Malformed tool call recovery and loop guard feedback** are relatively rare events that inject unexpected user/tool messages, breaking the expected alternation pattern.

---

## Architecture Notes

### Message Flow for Each LLM Call
```
1. System Prompt = Builder.Build() (static per session) + ThinkingInstruction (static per session unless changed)
2. Tools = ConvertToolsToLLM(agentCtx.Tools) (static per session)
3. Messages:
   a. agentCtx.RecentMessages (grows each turn)
   b. BeforeModelHook injections (appended to RecentMessages)
   c. ConvertMessagesToLLM → filter by IsAgentVisible(), "toolResult"→"tool"
   d. injectRuntimeMeta → build runtime_state YAML
   e. insertBeforeLastUserMessage(runtimeMsg) → insert before last user message
```

### Cache-Friendly Design Patterns
- **Band-based runtime state updates** (not every turn)
- **Insert before last user message** (preserves earlier prefix)
- **Static tool definitions** (no dynamic loading/unloading)
- **Stable system prompt** (rebuilt only on session switch)

### Cache-Hostile Events
- **Compaction** (complete replacement)
- **Session switch** (complete replacement)
- **ThinkingInstruction change** (system prompt suffix change)
- **Hook output variation** (unpredictable)
- **Malformed tool recovery** (unexpected user message insertion)
- **Loop guard feedback** (additional tool_result messages)

---

## MCP Status
**No MCP code exists in this codebase.** There is no dynamic tool loading/unloading. The tool registry (`app.registry.All()`) is set at startup and remains fixed for the session lifetime.

---

## Completeness Checklist

### Source Files Examined
- [x] `pkg/context/message.go` — Message constructors (NewUserMessage, NewAssistantMessage, NewToolResultMessage, NewCompactionSummaryMessage, NewSystemMessage, CopyMessageWithKind)
- [x] `pkg/agent/llm_stream.go` — streamAssistantResponse() (main injection orchestration)
- [x] `pkg/agent/runtime_meta.go` — injectRuntimeMeta() (runtime_state YAML)
- [x] `pkg/agent/tool_guard.go` — Loop guard feedback + malformed tool call recovery
- [x] `pkg/agent/loop_state.go` — prepareLLMMessages() calls hooks, processes tool results
- [x] `pkg/agent/loop.go` — Inner loop: compaction, recovery, message flow
- [x] `pkg/agent/conversion.go` — ConvertMessagesToLLM (IsAgentVisible filter, role mapping)
- [x] `pkg/agent/hooks.go` — BeforeModelHook (fan-out), AfterToolHook (chain)
- [x] `pkg/agent/tool_exec.go` — Tool execution, result creation
- [x] `pkg/agent/tool_output.go` — Tool output truncation at execution time
- [x] `pkg/agent/agent.go` — Agent.Prompt/Steer/FollowUp, message injection
- [x] `pkg/agent/checkpoint_manager.go` — Checkpoint save (no message injection)
- [x] `pkg/agent/compaction_controller.go` — Compaction orchestration
- [x] `pkg/prompt/builder.go` — Build() from template, dynamic tools/skills/skillStats, ThinkingInstruction()
- [x] `pkg/compact/context_management.go` — Context management triggers (20%/33%/50%), truncation
- [x] `pkg/compact/compact.go` — Compaction logic
- [x] `pkg/session/entries.go` — CompactionSummaryPrefix/Suffix, BranchSummaryPrefix/Suffix
- [x] `pkg/session/compaction.go` — Session compaction, message refs
- [x] `cmd/ai/rpc_app.go` — createBaseContext(), restoreLLMContextFromCompaction()
- [x] `cmd/ai/rpc_handlers.go` — RPC handler setup
- [x] `cmd/ai/rpc_message_handlers.go` — Message-related RPC handlers
- [x] `cmd/ai/rpc_session_handlers.go` — Session switch, resume, fork, rewind
- [x] `cmd/ai/session_writer.go` — Session persistence, compaction
- [x] `pkg/tools/context_mgmt/update_llm_context.go` — update_llm_context tool

### Injection Categories Covered
- [x] System prompt dynamic appends (ThinkingInstruction, bootstrap files, skill index, project context)
- [x] Runtime state injection (runtime_state YAML + LLMContext)
- [x] Compaction summary messages (compactionSummary, branch summary)
- [x] Hook-injected messages (BeforeModelHook, AfterToolHook)
- [x] Context management truncation
- [x] Tool loop guard feedback
- [x] Malformed tool call recovery
- [x] Session resume/branch messages
- [x] Tool output truncation at execution time
- [x] MCP tool injection (confirmed: does not exist)
- [x] Checkpoint/recovery (confirmed: checkpoints don't inject messages — they restore state)

### Behaviors Verified
- [x] Multi-turn tool use loop
- [x] Band-based runtime meta update schedule
- [x] `insertBeforeLastUserMessage` positioning
- [x] `IsAgentVisible()` filtering
- [x] `toolResult` → `tool` role mapping in conversion
- [x] Tool output truncation (error-weighted vs standard)
- [x] Compaction flow (summary → message replacement → context rebuild)
- [x] Session resume flow (createBaseContext → restoreLLMContext → session replay)
- [x] Empty response retry (does not inject messages — just retries)
- [x] Context limit recovery (triggers compaction, same as #3)
# Codex Architecture Analysis - Key Takeaways

## Overview

OpenAI Codex is a production-grade AI coding agent system written in Rust. It uses a queue-based communication pattern with clear separation between the core agent logic and user interfaces.

## Key Architectural Patterns

### 1. Submission/Event Queue Pattern

Codex uses an asynchronous queue-based communication pattern:

```rust
// Clients submit operations
Codex::submit(Op)  // Enqueues operation with UUID

// Clients receive events
Codex::next_event()  // Blocks until next event available
```

**Benefits:**
- Non-blocking submission
- Streaming progress updates
- Interruptibility (can abort in-flight turns)

**Comparison with ai project:**
- ai uses RPC over stdin/stdout with streaming events
- Similar pattern but different transport

### 2. Layered Protocol Design

```
┌─────────────────────────────────────────────────────┐
│           External Client (TUI/App/CLI)             │
└─────────────────────────────────────────────────────┘
                          │
                   JSON-RPC 2.0 Lite
                          │
┌─────────────────────────────────────────────────────┐
│              app-server-protocol                    │
│  (Event translation, Thread history reconstruction)  │
└─────────────────────────────────────────────────────┘
                          │
              Submission/Event Queues
                          │
┌─────────────────────────────────────────────────────┐
│                  codex-core                         │
│  (Session management, Model client, Tool execution)  │
└─────────────────────────────────────────────────────┘
```

**Key Insight:** Clear separation between external protocol and internal core allows independent evolution.

### 3. Context Management

From the search results, Codex's `ContextManager` handles:

- **Token Estimation**: Coarse token counting using byte-based heuristics
- **Truncation Policy**: Applied when recording items
- **Normalization**: Preparing history for model prompts

**Configuration:**
```toml
[model]
model_context_window = 200000
model_auto_compact_token_limit = 150000  # Auto-compaction threshold
```

**Comparison with ai project:**

| Feature | Codex | ai project |
|---------|-------|------------|
| Token counting | Byte-based heuristics | Character/4 approximation |
| Compaction trigger | Token limit (150K/200K) | Message count (50) + token limit |
| Truncation policy | Applied at recording time | Applied when sending to LLM |

### 4. Message History Management

Codex separates two types of history:

1. **Global History**: Cross-session, user-focused, persisted to JSONL
2. **Thread Context**: Turn-level, model-focused, managed by `ContextManager`

**Key Events:**
- `GetHistoryEntryResponse` - Returns requested history entry
- `ContextCompacted` - Notification of history compaction
- `ThreadRolledBack` - Notification of user turn rollback

**Comparison with ai project:**

| Feature | Codex | ai project |
|---------|-------|------------|
| Storage | JSONL per thread | JSONL per session |
| History types | Global + Thread | Single message list |
| Compaction event | Explicit `ContextCompacted` | Implicit in message flow |

### 5. Compaction Strategy

From PR #11487 mentioned in updates:
- **Pre-turn auto-compaction** - Before each LLM request
- **Pre-turn failure recovery** - When context limit exceeded
- **Mid-turn continuation compaction** - During long responses
- **Manual `/compact`** - User-initiated

**Test Coverage:**
- Reusable context snapshot helpers in `core/tests/common/context_snapshot.rs`
- Coverage for local and remote compaction flows

**Key Design Decision:** Keeps runtime logic separate from test coverage.

**Comparison with ai project:**

| Feature | Codex | ai project |
|---------|-------|------------|
| Compaction timing | Pre-turn, mid-turn, manual | Pre-LLM + error recovery |
| Test infrastructure | Dedicated snapshot helpers | Inline tests |
| Model switch handling | Strips `<model_switch>`, re-appends | Not mentioned |

### 6. Tool Output Handling

Codex does NOT appear to send tool output summaries to the LLM. Based on the architecture:

- Tool outputs are part of `ResponseItem` flow
- Compaction handles old tool outputs
- No "assistant" messages with summaries visible to LLM

**This is the key difference from ai project!**

## Recommendations for ai Project

### 1. Fix Tool Summary Visibility (Priority 1)

**Current Problem:** Tool summaries are `AgentVisible = true`, causing LLM to learn the pattern.

**Codex Approach:** Tool outputs are managed through compaction, not as visible assistant messages.

**Recommended Fix:**
```go
// Make tool summaries invisible to LLM
func newToolSummaryContextMessage(text string) AgentMessage {
    msg := NewAssistantMessage()
    msg.Content = []agent.ContentBlock{
        TextContent{Type: "text", Text: text},
    }
    return msg.WithVisibility(false, true).WithKind("tool_summary")
}
```

### 2. Separate User-Visible vs LLM-Visible History

**Codex Pattern:** Two separate history views
- Thread history for model (normalized, compacted)
- Global history for user (complete, persisted)

**ai Project:** Currently uses single message list with visibility flags.

**Potential Improvement:**
```go
type AgentContext struct {
    // Messages for LLM (compacted, filtered)
    LLMMessages []AgentMessage

    // Complete history for persistence
    FullHistory []AgentMessage
}
```

### 3. Explicit Compaction Events

**Codex Pattern:** `ContextCompacted` event explicitly notifies of compaction.

**ai Project:** Compaction happens silently.

**Potential Improvement:**
```go
type CompactionEvent struct {
    BeforeCount int
    AfterCount  int
    Method      string // "pre_llm", "context_limit", "manual"
    Timestamp   int64
}
```

### 4. Configurable Auto-Compaction Threshold

**Codex Configuration:**
```toml
model_auto_compact_token_limit = 150000  # 75% of context window
```

**ai Project:** Currently `MaxMessages: 50` + `MaxTokens: 8000`.

**Potential Improvement:**
```go
type Compactor struct {
    // Percentage of context window to trigger compaction
    autoCompactThreshold float64 // e.g., 0.75 for 75%
}
```

### 5. Token Counting Accuracy

**Codex:** Uses byte-based heuristics (mentioned as "coarse" but functional)

**ai Project:** Character/4 approximation

**Consider:** Use actual tokenizer for critical path:
```go
func EstimateTokensPrecise(text string, model string) int {
    // Use tiktoken or similar for accurate counting
}
```

### 6. Test Infrastructure for Compaction

**Codex:** Dedicated `context_snapshot.rs` with reusable helpers.

**ai Project:** Tests scattered in individual files.

**Consider:**
```go
// pkg/compact/testing/snapshot.go
package testing

type ContextSnapshot struct {
    Before []AgentMessage
    After  []AgentMessage
    Config *Config
}

func (s *ContextSnapshot) AssertCompacted(t *testing.T) {
    // Reusable assertions
}
```

## What Codex Does NOT Do (That ai Does)

### 1. Tool Summary Messages

Codex does NOT create assistant messages with tool output summaries. Tool outputs are either:
- Kept as-is (recent)
- Compacted away (old)
- Never sent as "summary" messages to the LLM

### 2. Async Tool Summarization

Codex handles compaction synchronously during turn execution, not via background goroutines.

**Potential Issue in ai:** The async summarizer adds messages while the loop is running, which could cause race conditions with message list access.

## Architecture Comparison Summary

| Aspect | Codex | ai project |
|--------|-------|------------|
| Language | Rust | Go |
| Communication | Submission/Event queues | RPC over stdin/stdout |
| Concurrency | Async/await (tokio) | Goroutines + channels |
| History storage | JSONL per thread | JSONL per session |
| Tool output handling | Direct in history, compacted | Summarized into assistant messages |
| Compaction trigger | Token threshold | Message count + token limit |
| Test infrastructure | Dedicated snapshot helpers | Inline |

## Key Takeaway

The **most important lesson** from Codex is:

**Never send tool summary messages to the LLM as assistant messages.**

Tool outputs should be:
1. Kept in history as `tool` role messages
2. Compacted/removed when old
3. Never reformatted as `assistant` messages with summaries

This prevents the pattern poisoning issue we discovered in the ai project.

## Files Referenced

- `codex-rs/core/src/context_manager/history.rs` - History management
- `codex-rs/core/src/state/session.rs` - Session state
- `codex-rs/protocol/src/protocol.rs` - Protocol definitions
- `codex-rs/core/tests/common/context_snapshot.rs` - Test helpers

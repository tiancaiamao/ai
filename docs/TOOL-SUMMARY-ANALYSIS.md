# Tool Summary Issue Analysis and Proposed Fixes

## Issue Summary

After analyzing the LLM request payload (`/tmp/xxx.jsonl`) and session file, we discovered that the LLM is generating `[Tool outputs summary]` messages instead of continuing with tool calls. This is caused by **pattern poisoning** - the LLM learned an incorrect pattern from the tool summary messages in its conversation history.

## Key Findings

### 1. Message Structure Anomaly
The request payload contained **510 messages** with an extremely abnormal distribution:
- system: 1
- user: 10
- **assistant: 498** ⚠️
- tool: 1

### 2. Tool Summary Messages Are Agent-Visible

In `pkg/agent/tool_summary.go` and `pkg/agent/tool_summary_async.go`:

```go
func newToolSummaryContextMessage(text string) AgentMessage {
    msg := NewAssistantMessage()
    msg.Content = []agent.AgentContentBlock{
        agent.TextContent{Type: "text", Text: text},
    }
    return msg.WithVisibility(true, false).WithKind("tool_summary")
    //                       ^^^ LLM can see this!
}
```

These messages have `AgentVisible = true`, meaning they are sent to the LLM in subsequent requests.

### 3. LLM Learned the Wrong Pattern

From the session file, we found that the LLM started outputting its own `[Tool outputs summary]` messages:

| Message ID | stopReason | hasToolCalls | Description |
|------------|-----------|--------------|-------------|
| eaf9e910 | tool_calls | 1 | LLM outputs summary + 1 tool call |
| 60b15b28 | tool_calls | 2 | LLM outputs summary + 2 tool calls |
| 4a9e05ac | tool_calls | 2 | LLM outputs summary + 2 tool calls |
| e3f18ac4 | **stop** | **0** | LLM outputs summary and ends conversation |

The LLM saw many messages with this format in its history:
```
[Tool outputs summary]
Count: 2
Call IDs: call_35dafdc8d0ff4c1794be811f, call_456bfaa6f96244ce8ec1c285
- **call_35dafdc8d0ff4c1794be811f** (bash): ok — Found ...
```

And learned to mimic this pattern.

### 4. No Race Condition Found

The issue is NOT a concurrent race condition. The tool summary messages are correctly added by the async summarizer, and the LLM simply learned from them.

### 5. Compaction Status

No clear evidence of compaction being triggered. The default `MaxMessages: 50` should trigger compaction, but it may not have been enabled or the context window may be large enough to avoid triggering it.

## Root Cause

**Tool summary messages are designed to be assistant messages that are visible to the agent (LLM).** This causes the LLM to:
1. See the summary format in its conversation history
2. Learn that this is a valid output format
3. Start outputting summaries in the same format
4. Eventually end the conversation with a summary instead of continuing work

## Proposed Fixes

### Option A: Make Tool Summary Messages Invisible to LLM (Recommended)

**File**: `pkg/agent/tool_summary.go`

```go
func newToolSummaryContextMessage(text string) AgentMessage {
    msg := agent.NewAssistantMessage()
    msg.Content = []agent.ContentBlock{
        agent.TextContent{Type: "text", Text: text},
    }
    // Changed: Make invisible to LLM, only visible to users
    return msg.WithVisibility(false, true).WithKind("tool_summary")
}
```

**Pros**:
- Simple change
- Tool summaries still shown to users in the UI
- Prevents LLM from learning the pattern

**Cons**:
- LLM won't see any context about what was summarized

**File**: `pkg/agent/compact/compact.go` (also has `newToolSummaryContextMessage`)

Apply the same change.

### Option B: Filter Tool Summary Messages During LLM Conversion

**File**: `pkg/agent/conversion.go`

```go
func ConvertMessagesToLLM(ctx context.Context, messages []AgentMessage) []llm.LLMMessage {
    // ...
    for _, msg := range messages {
        if !msg.IsAgentVisible() {
            continue
        }
        // NEW: Filter out tool summary messages
        if msg.Metadata != nil && msg.Metadata.Kind == "tool_summary" {
            continue
        }
        // ... rest of conversion
    }
    // ...
}
```

**Pros**:
- Centralized filtering
- Doesn't change the message creation logic
- Easier to add conditions

**Cons**:
- Adds overhead to every message conversion

### Option C: Use a Different Message Type for Tool Summaries

Create a new message type that is explicitly not sent to the LLM.

**File**: `pkg/agent/message.go`

Add a new role or use existing metadata to mark these as "metadata only" messages.

**Pros**:
- More explicit design
- Clear separation of concerns

**Cons**:
- More invasive changes
- May require updates to multiple files

### Option D: Add System Prompt Warning

Add explicit instructions to the system prompt:

```
IMPORTANT: You are an assistant, not a tool summarizer.
Do NOT output messages in the format of "[Tool outputs summary]".
Continue working until the task is complete.
```

**Pros**:
- Non-invasive
- Can help with other similar issues

**Cons**:
- Doesn't address the root cause
- LLM may still learn patterns from few-shot examples

### Option E: Compact Tool Summary Messages More Aggressively

Ensure that tool summary messages are compacted/removed from history more frequently.

**File**: `pkg/agent/compact/compact.go`

Modify compaction logic to specifically target `tool_summary` messages.

**Pros**:
- Reduces token usage
- Limits exposure to the pattern

**Cons**:
- Doesn't fully solve the problem
- Summaries would still be visible until compaction

## Recommended Approach (Updated)

**After deeper analysis, making summaries invisible to LLM would cause information loss.**

### Information Flow Analysis

```
┌────────────────────────────────────────────────────────────────────┐
│  Tool Output (truncated)                                          │
│  AgentVisible=true, UserVisible=true → LLM and user can see      │
└────────────────────────────────────────────────────────────────────┘
                            ↓ archiveToolResult()
┌────────────────────────────────────────────────────────────────────┐
│  Archived Tool Output                                             │
│  AgentVisible=false, UserVisible=true → Only user can see        │
└────────────────────────────────────────────────────────────────────┘
                            ↓ newToolBatchSummaryMessage()
┌────────────────────────────────────────────────────────────────────┐
│  Tool Summary Message                                             │
│  AgentVisible=true, UserVisible=false → Only LLM can see         │
│  Format: "[Tool outputs summary]\nCount: 2\n..."                │
└────────────────────────────────────────────────────────────────────┘
```

**If we make summaries invisible to LLM**:
- User sees archived tool output (good)
- LLM sees neither archived output NOR summary (information loss!)

### Updated Recommendation: Change Summary Format

**Option F (NEW - Recommended): Change Summary Format**

The problem is that the summary format looks like something the LLM should output:

```
[Tool outputs summary]
Count: 2
Call IDs: call_xxx, call_yyy
- **call_xxx** (bash): ok — Found ...
```

**Solution**: Change to a format that doesn't look like LLM output:

```go
func newToolBatchSummaryMessage(results []AgentMessage, summary string) AgentMessage {
    // Changed format: no markdown-style headers
    text := fmt.Sprintf("// Previous tool outputs (summarized): %s", summary)
    return newToolSummaryContextMessage(text)
}
```

Or even more explicit:

```go
func newToolBatchSummaryMessage(results []AgentMessage, summary string) AgentMessage {
    // Very explicit: this is context, not output
    text := fmt.Sprintf("[ARCHIVED_TOOL_CONTEXT: %s]", summary)
    return newToolSummaryContextMessage(text)
}
```

**Benefits:**
- Preserves information for LLM
- Format clearly indicates it's context, not output
- Doesn't look like something LLM should mimic

**Combined with Option D**: Add system prompt warning

```
NOTE: Messages starting with [ARCHIVED_TOOL_CONTEXT] are system-added
context from previous tool outputs. Do NOT imitate this format.
Continue working until the task is complete.
```

### Alternative: User Message Approach

**Option G**: Create summary as user message instead of assistant:

```go
func newToolBatchSummaryMessage(results []AgentMessage, summary string) AgentMessage {
    text := fmt.Sprintf("// Previous tool outputs: %s", summary)
    return NewUserMessage(text).WithVisibility(true, false).WithKind("tool_summary")
}
```

**Benefits:**
- LLM won't try to imitate "user" messages
- Clear signal that this is input, not output

**Drawback:**
- May confuse the conversation flow (user messages in middle of turns?)

### Summary of Options

| Option | Keeps Info | Prevents Pattern | Complexity | Recommended |
|--------|-----------|------------------|------------|-------------|
| A - Invisible to LLM | ❌ No | ✅ Yes | Low | ❌ Info loss |
| B - Filter in conversion | ❌ No | ✅ Yes | Medium | ❌ Info loss |
| F - Change format | ✅ Yes | ✅ Yes | Low | ✅ **Best** |
| G - User message | ✅ Yes | ✅ Yes | Low | ✅ Good |
| D - System prompt only | ✅ Yes | ⚠️ Maybe | Low | ⚠️ Weak |

### Final Recommendation

**Primary: Option F** - Change summary format to not look like LLM output
**Secondary: Option D** - Add system prompt warning
**Tertiary: Consider Option G** - User message approach if F doesn't work

## Files to Modify

1. `pkg/agent/tool_summary.go` - `newToolSummaryContextMessage()`
2. `pkg/agent/tool_summary_async.go` - (uses the same function)
3. `pkg/agent/compact/compact.go` - `newToolSummaryContextMessage()`
4. `pkg/agent/conversion.go` - Optional: add explicit filtering

## Testing Checklist

After implementing the fix:
- [ ] Run a long conversation that triggers tool summarization
- [ ] Verify tool summaries appear in the UI (for users)
- [ ] Verify tool summaries do NOT appear in LLM request payloads
- [ ] Verify LLM does not output `[Tool outputs summary]` format
- [ ] Verify agent continues work until completion
- [ ] Check that archived tool results are still properly hidden

## Related Code Locations

- `pkg/agent/tool_summary.go:365-371` - Creates tool summary messages
- `pkg/agent/tool_summary_async.go:267-317` - Async tool summarizer
- `pkg/agent/tool_summary_async.go:343-356` - Creates batch summary messages
- `pkg/agent/compact/compact.go:698-704` - Compaction tool digest messages
- `pkg/agent/conversion.go:14-84` - Message conversion for LLM
- `pkg/agent/loop.go:212-214` - Calls `applyReady()` before each LLM request

## Additional Observations

### Message Deduplication
The code has deduplication logic in `conversion.go` but it uses content hash, so different batch summaries won't be deduplicated.

### Token Estimation
Tool summaries help reduce tokens, but if they're sent to the LLM, they may actually increase total tokens because the LLM learns to output its own summaries.

### Async Summarizer
The async summarizer runs in a background goroutine and adds summaries to the conversation. The `applyReady()` call at the start of each loop iteration ensures summaries are applied before the next LLM request.

# Context Snapshot Architecture Design

## Overview

This document describes the new context management architecture for the AI agent. The architecture replaces the previous continuous LLM-driven context management model with an event-driven, staged approach.

**Key Design Philosophy:**

- **Event Log + Snapshot**: Messages are immutable logs; the active context is a reconstructed snapshot
- **Two-Mode Operation**: Normal mode (task execution) and Context Management mode (context reshaping)
- **LLM-Driven Decisions**: System monitors triggers, LLM makes context management decisions
- **Structured Context**: Maintained as LLMContext + RecentMessages + AgentState, not linear message history
- **User-Wait Trigger**: When context management is triggered, user response is paused until management completes

## Architecture

### High-Level Structure

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        Event Log + Snapshot                              │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  Persistence Layer                 Memory Layer              LLM Input    │
│  ────────────────                 ─────────────            ─────────    │
│  messages.jsonl ──apply logs───▶  ContextSnapshot  ──render──▶ Request    │
│  (immutable)      (incremental)     │                                │
│                                          │                                │
│  checkpoints/                      ┌───┴──────────────────────┐        │
│  ├── checkpoint_00015/            │  • LLMContext            │        │
│  │   ├── llm_context.txt         │  • RecentMessages        │        │
│  │   └── agent_state.json        │  • AgentState            │        │
│  ├── checkpoint_00030/            └──────────────────────────┘        │
│  │   ├── llm_context.txt                                                │
│  │   └── agent_state.json                                               │
│  └── checkpoint_00045/                                                 │
│        ├── llm_context.txt                                              │
│        └── agent_state.json                                             │
│                                                                          │
│  current/ ────► symlink to ────► checkpoint_00045/                      │
│  (platform-specific: symlink on Unix, junction on Windows)              │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

### Snapshot Components

The `ContextSnapshot` in memory consists of three parts:

```go
type ContextSnapshot struct {
    // 1. LLMContext - LLM-maintained structured context
    LLMContext string

    // 2. RecentMessages - Recent conversation history
    RecentMessages []AgentMessage

    // 3. AgentState - System-maintained metadata
    AgentState AgentState
}
```

#### LLMContext (LLM-Maintained)

Free-form text maintained by the LLM during context management. Suggested format:

```markdown
## Current Task
<one sentence description>
Status: <in_progress|completed|blocked>

## Completed Steps
<bullet list of completed items>

## Next Steps
<bullet list of next actions>

## Key Files
<filename>: <brief description>

## Recent Decisions
- <decision> (reason: <why>)

## Open Issues
- <issue> (status: <open|resolved>)
```

#### RecentMessages (System-Maintained)

Array of recent messages organized into two regions:

```
RecentMessages Pool:
┌─────────────────────────────────────────────┐
│  Protected Region (recent N)                │  ← Never truncated
│  [msg_N] [msg_N-1] ... [msg_1]             │
├─────────────────────────────────────────────┤
│  Candidate Region (stale messages)          │  ← LLM decides which to truncate
│  [msg_old_1] [msg_old_2] ... [msg_old_M]   │
└─────────────────────────────────────────────┘
```

**During normal operation:**
- New messages are appended
- No modification of existing messages

**During context management:**
- Recent N messages (Protected Region) are never truncated
- Stale messages beyond recent N (Candidate Region) are candidates for truncation
- **LLM decides** which stale messages to truncate via `truncate_messages` tool
- System only **executes** LLM's truncation decisions (does not decide itself)

**Responsibility Division:**

| Action | Decided By | Notes |
|--------|------------|-------|
| When to enter context management | System | Based on trigger conditions |
| Which messages to truncate | **LLM only** | Via `truncate_messages` tool |
| Protecting recent N messages | System | Enforced constraint |
| Executing truncation | System | Implements LLM's decision |

#### AgentState (System-Maintained)

```go
type AgentState struct {
    // Workspace
    WorkspaceRoot     string
    CurrentWorkingDir string

    // Statistics
    TotalTurns        int
    TokensUsed        int
    TokensLimit       int

    // Tracking
    LastLLMContextUpdate int  // Last turn when LLMContext was updated
    LastCheckpoint       int  // Last turn when checkpoint was created
    LastTriggerTurn      int  // Last turn when context management was triggered
    TurnsSinceLastTrigger int // Turns elapsed since last trigger

    // Active tool calls (for pairing protection)
    ActiveToolCalls     []string

    // Metadata
    SessionID      string
    CreatedAt      time.Time
    UpdatedAt      time.Time
}
```

## Mode Switching State Machine

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          Mode Switching Flow                               │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│   User Input                                                                 │
│      │                                                                       │
│      ▼                                                                       │
│ ┌────────────┐    Check Trigger        ┌──────────────────┐                │
│ │  Normal    │ ◀───────────────────── │  Trigger Check    │                │
│ │  Mode      │ ──────────────────────▶ │                   │                │
│ └────────────┘    Conditions             └──────────────────┘                │
│      ▲                                                              │         │
│      │                                                              │         │
│      │                                                              ▼         │
│      │                                                       ┌─────────────┐  │
│      │                                                       │ Triggered?  │  │
│      │                                                       └──────┬──────┘  │
│      │                                                              │         │
│      │                                    ┌─────────────────────────┴─────┐       │
│      │                                    │                               │       │
│      │                              No │                               │ Yes    │
│      │                                ▼                               ▼        │
│      │                        Continue Normal              ┌─────────────┐  │
│      │                        - Call LLM                 │ Context     │  │
│      │                        - Return response        │ Mgmt Mode   │  │
│      │                        - Increment turn         └──────┬──────┘  │
│      │                                                           │         │
│      │                                                           ▼         │
│      │                                                   ┌───────────────┐        │
│      │                                                   │ User Waits    │        │
│      │                                                   │ (pause input) │        │
│      │                                                   └───────┬───────┘        │
│      │                                                           │                 │
│      │                                                           ▼                 │
│      │                                                   ┌───────────────┐        │
│      │                                                   │ Execute      │        │
│      │                                                   │ Context      │        │
│      │                                                   │ Mgmt         │        │
│      │                                                   │ (LLM decides)│        │
│      │                                                   └───────┬───────┘        │
│      │                                                           │                 │
│      │                                                           ▼                 │
│      │                                                   ┌───────────────┐        │
│      │                                                   │ Apply Actions│        │
│      │                                                   │ - Update     │        │
│      │                                                   │   LLMContext │        │
│      │                                                   │ - Truncate   │        │
│      │                                                   │   messages   │        │
│      │                                                   └───────┬───────┘        │
│      │                                                           │                 │
│      │                                                           ▼                 │
│      │                                                   ┌───────────────┐        │
│      │                                                   │ Create        │        │
│      │                                                   │ Checkpoint    │        │
│      │                                                   └───────┬───────┘        │
│      │                                                           │                 │
│      │                                                           ▼                 │
│      │                              ┌─────────────────────────────┘       │
│      │                              │                                  │       │
│      │                              ▼                                  │       │
│      │                        Back to Normal                          │       │
│      │                        - Resume user input                    │       │
│      └──────────────────────────────────────────────────────────────┘       │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Critical Flow: Triggered Context Management

**When trigger conditions are met, user response is PAUSED:**

```
User sends message → Check triggers → Triggered?
                                              │
                                      No │                │ Yes
                                         ▼                  ▼
                                  Process normally   Context Mgmt Mode
                                  (call LLM,          (user waits)
                                   return result)
                                                           │
                                                           ▼
                                                    Execute context mgmt
                                                           │
                                                           ▼
                                                    Create checkpoint
                                                           │
                                                           ▼
                                                    Return to Normal
                                                           │
                                                           ▼
                                                    NOW process user msg
                                                           │
                                                           ▼
                                                    Return response
```

## Two Operating Modes

### Normal Mode (99% of the time)

**Purpose**: Execute user tasks

**System Prompt**: Task-focused, no context management instructions

**Available Tools**: All tools (read, write, bash, grep, etc.)

**Flow**:
```
1. Receive user input
2. Check trigger conditions
3. If not triggered:
   a. Append user message to RecentMessages
   b. Build LLM request from full snapshot
   c. Call LLM
   d. Append LLM response to RecentMessages
   e. Increment turn count
   f. Persist to messages.jsonl
   g. Return response to user
4. If triggered:
   a. Do NOT append user message yet
   b. Switch to Context Management mode
   c. Execute context management flow
   d. After context management completes:
      - Now append the pending user message
      - Process it in Normal mode
      - Return response
```

### Context Management Mode (Triggered)

**Purpose**: Reshape and optimize context

**System Prompt**: Context management focused (see below)

**Available Tools**:
- `update_llm_context`: Update LLMContext with new content
- `truncate_messages`: Truncate stale tool outputs
- `no_action`: Skip this cycle

**Important**: User input is PAUSED during context management.

**Flow**:
```
1. Build context management input (see below)
2. Call LLM with context management prompt and restricted tool set
3. Execute tool calls:
   - update_llm_context: Save new LLMContext to checkpoint
   - truncate_messages: Mark messages as truncated, record truncate event to log
   - no_action: Nothing to do (no checkpoint created)
4. If any action was taken (not no_action):
   a. Create checkpoint directory
   b. Save LLMContext to llm_context.txt
   c. Save AgentState to agent_state.json
   d. Update checkpoint_index.json
   e. Update current/ symlink/junction
5. If no_action was taken:
   a. Update AgentState.LastTriggerTurn (for minInterval enforcement)
   b. Do NOT create checkpoint
   c. Return to Normal mode
6. Return to Normal mode
7. Now process the pending user message
```

**Note on `no_action` behavior**:
- When LLM calls `no_action`, `LastTriggerTurn` is still updated to enforce `minInterval` before next trigger
- No checkpoint is created, but the trigger is "consumed"
- This prevents repeated triggers when context is healthy

## System Prompts

### Normal Mode System Prompt

```markdown
You are a helpful coding assistant. Help users with their programming tasks.

<capabilities>
- Read and write files
- Run commands
- Search code
- Analyze problems
- Debug issues
</capabilities>

<guidelines>
- Respond concisely
- Focus on the task at hand
- Show reasoning when helpful
- Ask for clarification when needed
</guidelines>

The system will automatically manage context size. You don't need to worry about token limits.
```

### Context Management Mode System Prompt

```markdown
<system mode="context_management">

You are in CONTEXT MANAGEMENT MODE. Your task is to review and reshape the conversation context.

⚠️ IMPORTANT: This is NOT a normal conversational turn. Do NOT respond to any user message.

<current_state>
{{template "current_state"}}
</current_state>

<context_to_review>

## Current LLM Context
{{llm_context}}

## Stale Tool Outputs (candidates for truncation)
{{stale_tool_outputs}}

## Recent Messages (last {{recent_count}})
{{recent_messages}}

</context_to_review>

<instructions>
Review the provided context and decide what action to take.

AVAILABLE ACTIONS:
1. **update_llm_context** - Rewrite the LLM Context to reflect current state
2. **truncate_messages** - Remove old tool outputs to save space
3. **no_action** - Context is healthy, no action needed

DECISION GUIDELINES:

**When to use update_llm_context:**
- Task has progressed or changed
- New files have been introduced
- Decisions have been made
- Errors were encountered or resolved
- Completed steps should be recorded

**When to use truncate_messages:**
- Old exploration outputs (grep, find) are no longer needed
- Large file reads that are no longer relevant
- Completed task results that won't be referenced again
- Duplicate or redundant outputs

**When to use no_action:**
- Context is healthy (tokens < 30%)
- No stale outputs to remove
- Recently created checkpoint

**TRUNCATION PRIORITIES:**
1. Exploration outputs (grep, find, locate)
2. Large file reads (>2000 chars)
3. Completed task results
4. Preserve: current task data, recent decisions, active work

**STALE SCORE REFERENCE:**
- Higher stale value = older output
- stale >= 10: Consider truncation
- stale >= 20: High priority for truncation

If you choose update_llm_context, provide a new LLM Context following this template:

```markdown
## Current Task
<one sentence description>
Status: <in_progress|completed|blocked>

## Completed Steps
<bullet list of completed items, each on one line>

## Next Steps
<bullet list of next actions, each on one line>

## Key Files
- <filename>: <brief description>
- <filename>: <brief description>

## Recent Decisions
- <decision made> (reason: <why it was made>)
- <decision made> (reason: <why it was made>)

## Open Issues
- <issue description> (status: <open|resolved|in_progress>)
```

Keep the LLM Context concise but complete. Aim for 500-1000 tokens.

</instructions>

</system>
```

## Trigger Conditions

Trigger conditions are hardcoded initially for simplicity. Modules make it easy to adjust later.

### Trigger Configuration

```go
type TriggerChecker struct {
    // Configuration (hardcoded initially)
    intervalTurns      int     = 10  // Check every 10 turns
    minTurns           int     = 5   // Don't trigger before turn 5
    tokenThreshold     float64 = 0.40 // 40% token usage
    tokenUrgent        float64 = 0.75 // 75% urgent mode
    staleCount         int     = 15  // 15 stale outputs
    minInterval        int     = 3   // Min 3 turns between normal triggers
}
```

### Trigger Logic

```go
func (t *TriggerChecker) ShouldTrigger(snapshot *ContextSnapshot) (should bool, urgency string) {
    state := snapshot.AgentState

    // Check minimum turn requirement
    if state.TotalTurns < t.minTurns {
        return false, ""
    }

    // URGENT mode: token usage critical - ignore minInterval
    tokenPercent := t.estimateTokenPercent(snapshot)
    if tokenPercent >= t.tokenUrgent {
        return true, "urgent"
    }

    // Check minimum interval for normal triggers
    if state.TurnsSinceLastTrigger < t.minInterval {
        return false, ""
    }

    // Normal trigger conditions
    staleCount := t.countStaleOutputs(snapshot)

    if tokenPercent >= t.tokenThreshold && staleCount >= 10 {
        return true, "normal"
    }

    if tokenPercent >= 0.30 {
        return true, "normal"
    }

    if state.TotalTurns >= 15 && tokenPercent >= 0.25 {
        return true, "normal"
    }

    if staleCount >= t.staleCount {
        return true, "normal"
    }

    // Skip condition: context is healthy
    if state.TotalTurns >= 20 && tokenPercent < 0.30 {
        return false, "skip"
    }

    // Periodic check
    if state.TotalTurns % t.intervalTurns == 0 {
        return true, "periodic"
    }

    return false, ""
}
```

### Trigger Matrix

| Condition | Action | Urgency | Notes |
|-----------|--------|---------|-------|
| tokens >= 75% | Context management | urgent | Ignores minInterval |
| tokens >= 40% AND stale >= 10 | Context management | normal | Standard trigger |
| tokens >= 30% | Context management | normal | Mild pressure |
| turns >= 15 AND tokens >= 25% | Context management | normal | Periodic maintenance |
| stale >= 20 | Context management | normal | Cleanup stale outputs |
| turns >= 20 AND tokens < 30% | Skip | - | Context healthy |
| turns % 10 == 0 | Context management | periodic | Routine check |

### Stale Calculation

The `stale` attribute indicates how old a tool output is:

```go
// Calculate stale score for tool results
// stale = total visible tool results - position in list (0-indexed from oldest)
// Higher stale = older output

func calculateStale(resultIndex int, totalVisibleToolResults int) int {
    if totalVisibleToolResults == 0 {
        return 0
    }
    return totalVisibleToolResults - resultIndex - 1
}
```

**Example**:
```
Current visible tool results (ordered oldest to newest):
[0] grep_result     → stale = 5 (newest)
[1] read_file       → stale = 4
[2] bash_output     → stale = 3
[3] another_grep    → stale = 2
[4] old_read        → stale = 1
[5] very_old_bash   → stale = 0 (oldest, truncate first)

totalVisible = 6
```

## File Structure

```
session_{id}/
├── messages.jsonl                    # Complete message log (append-only)
├── checkpoint_index.json             # Checkpoint manifest
├── current/                          # Platform-specific link to latest
│   ├── llm_context.txt               # LLM-maintained context
│   └── agent_state.json              # System state
└── checkpoints/
    ├── checkpoint_00015/
    │   ├── llm_context.txt
    │   └── agent_state.json
    ├── checkpoint_00030/
    │   ├── llm_context.txt
    │   └── agent_state.json
    └── checkpoint_00045/
        ├── llm_context.txt
        └── agent_state.json
```

### current/ Implementation

The `current/` directory provides quick access to the latest checkpoint. Implementation varies by platform:

**Unix/Linux/macOS:**
```bash
current -> checkpoints/checkpoint_00045/
```
- Use symbolic link
- Created with: `ln -s checkpoints/checkpoint_00045 current`
- Updated with: `ln -sfn checkpoints/checkpoint_00045 current`

**Windows:**
```
current\ (junction pointing to checkpoints\checkpoint_00045\)
```
- Use junction (requires admin privileges) or
- Use symbolic link (Windows 10+ with developer mode)

**Cross-platform abstraction:**
```go
func updateCurrentLink(checkpointPath string) error {
    currentPath := filepath.Join(sessionDir, "current")

    // Remove existing link/dir
    os.RemoveAll(currentPath)

    // Create platform-specific link
    if runtime.GOOS == "windows" {
        return os.Symlink(checkpointPath, currentPath)
    }
    return os.Symlink(checkpointPath, currentPath)
}
```

### checkpoint_index.json

```json
{
  "latest_checkpoint_turn": 45,
  "latest_checkpoint_path": "checkpoints/checkpoint_00045/",
  "checkpoints": [
    {
      "turn": 15,
      "message_index": 50,
      "path": "checkpoints/checkpoint_00015/",
      "created_at": "2024-03-31T10:00:00Z",
      "llm_context_chars": 850,
      "recent_messages_count": 8
    },
    {
      "turn": 30,
      "message_index": 100,
      "path": "checkpoints/checkpoint_00030/",
      "created_at": "2024-03-31T10:15:00Z",
      "llm_context_chars": 920,
      "recent_messages_count": 12
    },
    {
      "turn": 45,
      "message_index": 150,
      "path": "checkpoints/checkpoint_00045/",
      "created_at": "2024-03-31T10:30:00Z",
      "llm_context_chars": 1050,
      "recent_messages_count": 15
    }
  ]
}
```

## Context Management Tools

### update_llm_context

Updates the LLMContext with new content.

**Parameters**:
```json
{
  "llm_context": "string (required) - New LLMContext in markdown format"
}
```

**Implementation**:
```go
func (t *UpdateLLMContextTool) Execute(ctx context.Context, params map[string]any) ([]ContentBlock, error) {
    llmContext, ok := params["llm_context"].(string)
    if !ok || llmContext == "" {
        return nil, fmt.Errorf("llm_context is required and must be non-empty")
    }

    // Get current checkpoint directory
    checkpointDir := createCheckpointDir()

    // Save llm_context.txt
    llmContextPath := filepath.Join(checkpointDir, "llm_context.txt")
    if err := os.WriteFile(llmContextPath, []byte(llmContext), 0644); err != nil {
        return nil, fmt.Errorf("failed to write llm_context.txt: %w", err)
    }

    traceevent.Log(ctx, "context_mgmt_llm_context_updated",
        traceevent.Field{Key: "chars", Value: len(llmContext)},
        traceevent.Field{Key: "checkpoint_path", Value: checkpointDir},
    )

    return []ContentBlock{
        TextContent{Type: "text", Text: "LLM Context updated."},
    }, nil
}
```

### truncate_messages

Truncates old tool outputs to save context space.

**Parameters**:
```json
{
  "message_ids": "string (required) - Comma-separated tool call IDs to truncate"
}
```

**Implementation**:
```go
func (t *TruncateMessagesTool) Execute(ctx context.Context, params map[string]any) ([]ContentBlock, error) {
    idsRaw, ok := params["message_ids"].(string)
    if !ok || idsRaw == "" {
        return nil, fmt.Errorf("message_ids is required")
    }

    // Parse and validate IDs
    ids := strings.Split(idsRaw, ",")
    var validIDs []string
    for _, id := range ids {
        id = strings.TrimSpace(id)
        if id == "" {
            continue
        }
        if !isValidToolCallID(id) {
            traceevent.Log(ctx, "context_mgmt_invalid_id",
                traceevent.Field{Key: "id", Value: id},
            )
            continue
        }
        validIDs = append(validIDs, id)
    }

    if len(validIDs) == 0 {
        return nil, fmt.Errorf("no valid tool call IDs provided")
    }

    // Apply truncate
    count := t.applyTruncate(ctx, validIDs)

    traceevent.Log(ctx, "context_mgmt_messages_truncated",
        traceevent.Field{Key: "count", Value: count},
        traceevent.Field{Key: "ids", Value: strings.Join(validIDs, ",")})

    return []ContentBlock{
        TextContent{Type: "text", Text: fmt.Sprintf("Truncated %d messages.", count)},
    }, nil
}
```

### Truncate Marking

Messages are marked as truncated in memory:

```go
// Method 1: Set flag on message
type AgentMessage struct {
    ...
    Truncated     bool   `json:"truncated,omitempty"`
    TruncatedAt   int    `json:"truncated_at,omitempty"`
    OriginalSize  int    `json:"original_size,omitempty"`
}

// Method 2: Replace content with tag
func truncateMessage(msg AgentMessage) AgentMessage {
    originalSize := len(msg.ExtractText())

    msg.Content = []ContentBlock{
        TextContent{
            Type: "text",
            Text: fmt.Sprintf(
                `<agent:tool name="%s" chars="%d" truncated="true" />`,
                msg.ToolName,
                originalSize,
            ),
        },
    }
    msg.Truncated = true
    msg.OriginalSize = originalSize
    msg.TruncatedAt = currentTurn

    return msg
}

// Check if message is truncated
func (m AgentMessage) IsTruncated() bool {
    if m.Truncated {
        return true
    }
    content := m.ExtractText()
    return strings.Contains(content, `truncated="true"`)
}
```

### no_action

Indicates that no context management is needed this cycle.

**Parameters**: None

**Behavior**: No checkpoint is created, no changes made.

## Tool Output Format for LLM

**Mode-Specific Rendering**: Different modes use different rendering formats because LLM needs to see tool_call_id only in context management mode.

### Normal Mode Rendering

Standard protocol - tool_call_id is **hidden** from LLM:

```go
func RenderToolResult(msg AgentMessage, mode AgentMode) string {
    if mode == ModeNormal {
        // Standard rendering, hide ID
        return msg.Content
    }
    // ...
}
```

**What LLM sees**: Just the content, no ID or metadata.

### Context Management Mode Rendering

Special `<agent:tool>` format - tool_call_id is **exposed** to LLM:

```xml
<agent:tool id="call_abc123" name="bash" stale="5" chars="12345">
[tool output content]
</agent:tool>
```

**Fields**:
- `id`: Tool call ID for truncation reference (LLM uses this to call `truncate_messages`)
- `name`: Tool name
- `stale`: Staleness score (higher = older, see calculation below)
- `chars`: Original character count

**What LLM sees**: Can see `id`, `stale`, `chars` metadata and use `id` to call `truncate_messages`.

### Why Mode-Specific?

| Aspect | Normal Mode | Context Management Mode |
|--------|-------------|-------------------------|
| LLM needs tool_call_id? | ❌ No | ✅ Yes (for truncate) |
| Protocol format | Standard | Custom `<agent:tool>` tags |
| Metadata visible | No | stale, chars, id |

### Implementation

```go
func RenderToolResult(msg AgentMessage, mode AgentMode, stale int) string {
    if mode == ModeNormal {
        // Standard rendering, hide ID
        return msg.Content
    }

    if mode == ModeContextMgmt {
        // Special rendering, expose ID + metadata
        content := msg.Content

        // Handle large output preview
        if len(content) > ToolOutputMaxChars {
            head := content[:ToolOutputPreviewHead]
            tail := content[len(content)-ToolOutputPreviewTail:]
            truncatedChars := len(content) - ToolOutputPreviewHead - ToolOutputPreviewTail
            content = fmt.Sprintf("%s\n... (%d chars truncated) ...\n%s",
                head, truncatedChars, tail)
        }

        return fmt.Sprintf(
            `<agent:tool id="%s" name="%s" stale="%d" chars="%d">%s</agent:tool>`,
            msg.ToolCallID, msg.ToolName, stale, len(msg.Content), content,
        )
    }

    return msg.Content
}
```

## Building LLM Request from Snapshot

**Critical: Cache-Friendly Structure**

LLMContext should **NOT** be injected into system prompt. Use `<agent:xxx>` user messages instead to:
- Keep system prompt stable for better caching
- Maintain protocol compliance
- Allow flexible metadata injection

### Correct LLM Request Structure

```
┌─────────────────────────────────────────────────────┐
│  1. System Prompt (stable, cacheable)               │
│     "You are a helpful coding assistant..."         │
├─────────────────────────────────────────────────────┤
│  2. Recent Messages (history + recent)              │
│     [Messages from conversation...]                 │
├─────────────────────────────────────────────────────┤
│  3. Inject BEFORE last user message:                │
│     <agent:llm_context>                             │
│     <agent:runtime_state>                           │
├─────────────────────────────────────────────────────┤
│  4. Tool Schema (at the end)                        │
│     [Tool definitions...]                           │
└─────────────────────────────────────────────────────┘
```

### Build LLM Request Implementation

```go
func (a *Agent) buildLLMRequest(snapshot *ContextSnapshot, mode AgentMode) (*LLMRequest, error) {
    request := &LLMRequest{}

    // 1. System prompt (stable for caching)
    request.SystemPrompt = a.getSystemPrompt(mode)

    // 2. RecentMessages (only non-truncated, agent-visible)
    for _, msg := range snapshot.RecentMessages {
        if !msg.IsAgentVisible() {
            continue
        }
        if msg.IsTruncated() {
            continue
        }

        // Mode-specific rendering
        var content string
        if mode == ModeContextMgmt && msg.Role == "toolResult" {
            content = RenderToolResult(msg, mode, calculateStale(msg))
        } else {
            content = msg.Render()
        }

        request.Messages = append(request.Messages, LLMMessage{
            Role:    msg.Role,
            Content: content,
        })
    }

    // 3. Inject <agent:xxx> messages BEFORE last user message
    lastUserIndex := findLastUserMessageIndex(request.Messages)

    // Inject llm_context
    if snapshot.LLMContext != "" {
        llmContextMsg := LLMMessage{
            Role:    "user",
            Content: fmt.Sprintf("<agent:llm_context>\n%s\n</agent:llm_context>", snapshot.LLMContext),
        }
        request.Messages = insertBefore(request.Messages, lastUserIndex, llmContextMsg)
    }

    // Inject runtime_state (in both modes for visibility)
    runtimeStateMsg := LLMMessage{
        Role:    "user",
        Content: buildRuntimeStateXML(snapshot.AgentState),
    }
    request.Messages = insertBefore(request.Messages, lastUserIndex, runtimeStateMsg)

    // 4. Tools (mode-specific)
    request.Tools = a.getToolsForMode(mode)

    return request, nil
}

// insertBefore inserts a message at the specified index
func insertBefore(messages []LLMMessage, index int, newMsg LLMMessage) []LLMMessage {
    result := make([]LLMMessage, 0, len(messages)+1)
    result = append(result, messages[:index]...)
    result = append(result, newMsg)
    result = append(result, messages[index:]...)
    return result
}

// findLastUserMessageIndex finds the index of the last user message
func findLastUserMessageIndex(messages []LLMMessage) int {
    for i := len(messages) - 1; i >= 0; i-- {
        if messages[i].Role == "user" {
            return i
        }
    }
    return len(messages) // No user message found, append to end
}
```

This matches the existing `insertBeforeLastUserMessage` logic from `pkg/agent/loop.go:1707-1735`.

### <agent:llm_context> Format

```xml
<agent:llm_context>
## Current Task
Implementing feature X
Status: in_progress

## Completed Steps
- Created base structure
- Added core logic

## Next Steps
- Write tests
- Add documentation
</agent:llm_context>
```

### <agent:runtime_state> Format

```xml
<agent:runtime_state>
tokens_used: 45000
tokens_limit: 200000
tokens_percent: 22.5
recent_messages: 15
stale_outputs: 8
turn: 25
urgency: none
</agent:runtime_state>
```

### Benefits

| Benefit | Explanation |
|---------|-------------|
| **Cache-friendly** | System prompt stays stable |
| **Protocol-compliant** | Uses standard role=user messages |
| **Flexible** | Easy to add/remove metadata |
| **Compatible** | Works with standard LLM APIs |


## Event Sourcing: Truncate Operations as Log Events

**Key principle**: Truncate operations are recorded as events in the log, and replayed to build the final snapshot.

This is essentially **Event Sourcing**: state transitions are stored as a sequence of events, and current state is derived by replaying events.

### messages.jsonl Structure

`messages.jsonl` is an append-only event log that records two types of events:

```jsonl
{"type": "message", "message": {"role": "user", "content": "..."}}
{"type": "message", "message": {"role": "assistant", "content": "..."}}
{"type": "message", "message": {"role": "toolResult", "tool_call_id": "call_abc", "content": "原始内容"}}
{"type": "truncate", "truncate": {"tool_call_id": "call_abc", "turn": 15, "trigger": "context_management", "timestamp": "2024-03-31T10:30:00Z"}}
{"type": "message", "message": {"role": "user", "content": "..."}}
```

### Event Types

**1. Message Event** (`type="message"`)
```json
{
  "type": "message",
  "message": {
    "role": "user|assistant|toolResult",
    "content": "...",
    "tool_call_id": "call_abc",  // for tool results
    "tool_name": "bash"
  }
}
```

**2. Truncate Event** (`type="truncate"`)
```json
{
  "type": "truncate",
  "truncate": {
    "tool_call_id": "call_abc",
    "turn": 15,
    "trigger": "context_management",
    "timestamp": "2024-03-31T10:30:00Z"
  }
}
```

### ContextSnapshot Construction by Replay

```
Load Session = Base Checkpoint + Replay Journal

1. Load Checkpoint:
   ├─ llm_context.txt → LLMContext
   ├─ agent_state.json → AgentState
   └─ base_messages → RecentMessages[]

2. Replay messages.jsonl from checkpoint.message_index:
   ├─ type="message" → Append to RecentMessages
   └─ type="truncate" → Mark Truncated=true in RecentMessages

3. Final ContextSnapshot:
   ├─ LLMContext (string)
   ├─ RecentMessages[] (some marked Truncated)
   └─ AgentState
```

### Implementation

```go
// Journal entry represents a line in messages.jsonl
type JournalEntry struct {
    Type     string        `json:"type"` // "message" | "truncate"
    Message  *AgentMessage `json:"message,omitempty"`
    Truncate *TruncateEvent `json:"truncate,omitempty"`
}

type TruncateEvent struct {
    ToolCallID string `json:"tool_call_id"`
    Turn       int    `json:"turn"`
    Trigger    string `json:"trigger"` // "context_management" | "manual"
    Timestamp  string `json:"timestamp"`
}

// Build ContextSnapshot by replaying journal
func BuildContextSnapshot(checkpoint *Checkpoint, journal []*JournalEntry) *ContextSnapshot {
    snapshot := loadCheckpointData(checkpoint)

    for _, entry := range journal {
        if entry.Type == "message" {
            snapshot.RecentMessages = append(snapshot.RecentMessages, *entry.Message)
        } else if entry.Type == "truncate" {
            markTruncated(snapshot.RecentMessages, entry.Truncate.ToolCallID)
        }
    }

    return snapshot
}

func markTruncated(messages []AgentMessage, toolCallID string) {
    for i := range messages {
        if messages[i].ToolCallID == toolCallID {
            messages[i].Truncated = true
            // Preserve original content (don't replace with tag)
            // Truncated status is checked during rendering
            break
        }
    }
}
```

### Persistence Strategy

1. **Original messages** remain intact in the log (type="message" events)
2. **Truncate operations** are appended as separate events (type="truncate")
3. **No mutation** of existing log entries
4. **State is derived** by replaying events from checkpoint

### Rendering Logic

```go
// When building LLM request from ContextSnapshot
for _, msg := range snapshot.RecentMessages {
    if msg.IsTruncated() {
        // Skip truncated messages (or render truncated tag)
        continue  // or render: `<agent:tool ... truncated="true" />`
    }
    if !msg.IsAgentVisible() {
        continue
    }
    // Render message to LLM
    llmMessages = append(llmMessages, msg)
}
```

### Key Benefits

| Feature | Description |
|---------|-------------|
| **Traceable** | Every truncate operation has a log record |
| **Replayable** | Rebuild state from any checkpoint by replaying events |
| **Auditable** | messages.jsonl is complete operation history |
| **Debuggable** | Can see "what was truncated when and why" |

## Tool Selection by Mode

```go
func (a *Agent) getToolsForMode(mode AgentMode) []Tool {
    switch mode {
    case ModeNormal:
        return a.allTools  // All available tools
    case ModeContextMgmt:
        return []Tool{
            &UpdateLLMContextTool{},
            &TruncateMessagesTool{},
            &NoActionTool{},
        }
    default:
        return []Tool{}
    }
}
```

**Security**: By only registering the 3 context management tools in Context Management mode, the LLM cannot call other tools even if it tries to.

### Token Estimation

```go
func (s *ContextSnapshot) EstimateTokens() int {
    // 1. Priority: Use actual LLM usage from last request
    if s.AgentState.LastLLMUsage.TotalTokens > 0 {
        baseTokens := s.AgentState.LastLLMUsage.TotalTokens
        // Add messages since last LLM call
        newMessages := countMessagesSince(s, s.AgentState.LastLLMUsage.Turn)
        return baseTokens + estimateMessageTokens(newMessages)
    }

    // 2. Fallback: Estimate from snapshot
    llmContextTokens := len(s.LLMContext) / 4  // 1 token ≈ 4 chars
    messagesTokens := estimateMessageTokens(s.RecentMessages)
    stateTokens := 200  // Fixed overhead for AgentState

    return llmContextTokens + messagesTokens + stateTokens
}

func estimateMessageTokens(messages []AgentMessage) int {
    total := 0
    for _, msg := range messages {
        if !msg.IsAgentVisible() || msg.IsTruncated() {
            continue
        }
        // Rough estimate: 1 token per 4 characters
        total += len(msg.ExtractText()) / 4
    }
    return total
}
```

## Context Management Input Construction

When Context Management mode is triggered, the input to the LLM is constructed as:

```go
func (a *Agent) buildContextMgmtInput(snapshot *ContextSnapshot) string {
    var input strings.Builder

    // 1. Current state
    tokenPercent := a.estimateTokenPercent(snapshot)
    staleCount := a.countStaleOutputs(snapshot)

    input.WriteString("<current_state>\n")
    input.WriteString(fmt.Sprintf("Recent messages: %d\n", len(snapshot.RecentMessages)))
    input.WriteString(fmt.Sprintf("Tokens used: %d%%\n", tokenPercent))
    input.WriteString(fmt.Sprintf("Stale outputs: %d\n", staleCount))
    input.WriteString(fmt.Sprintf("Turns since last management: %d\n",
        snapshot.AgentState.TurnsSinceLastTrigger))
    input.WriteString(fmt.Sprintf("Urgency: %s\n", a.determineUrgency(snapshot)))
    input.WriteString("</current_state>\n\n")

    // 2. Current LLMContext
    if snapshot.LLMContext != "" {
        input.WriteString("## Current LLM Context\n")
        input.WriteString(snapshot.LLMContext)
        input.WriteString("\n\n")
    }

    // 3. Stale tool outputs (all visible tool results, ordered by stale)
    input.WriteString("## Stale Tool Outputs (candidates for truncation)\n")
    staleOutputs := a.getStaleToolOutputs(snapshot)
    for _, output := range staleOutputs {
        input.WriteString(output.RenderForLLM())
        input.WriteString("\n")
    }
    input.WriteString("\n")

    // 4. Recent messages (last N)
    input.WriteString(fmt.Sprintf("## Recent Messages (last %d)\n", RecentMessagesShowInMgmt))
    recent := a.getLastNMessages(snapshot.RecentMessages, RecentMessagesShowInMgmt)
    for _, msg := range recent {
        input.WriteString(msg.Render())
        input.WriteString("\n")
    }

    return input.String()
}
```

## Resume Flow

When resuming a session:

```go
func LoadSession(sessionID string, checkpointTurn int) (*ContextSnapshot, error) {
    // 1. Determine which checkpoint to load
    var checkpoint *CheckpointInfo
    if checkpointTurn <= 0 {
        // Load latest (current)
        checkpoint = loadLatestCheckpoint()
    } else {
        checkpoint = loadCheckpointAtTurn(checkpointTurn)
        if checkpoint == nil {
            return nil, fmt.Errorf("checkpoint not found at turn %d", checkpointTurn)
        }
    }

    // 2. Load checkpoint data
    llmContext, err := os.ReadFile(filepath.Join(checkpoint.Path, "llm_context.txt"))
    if err != nil {
        return nil, fmt.Errorf("failed to load llm_context.txt: %w", err)
    }

    agentStateData, err := os.ReadFile(filepath.Join(checkpoint.Path, "agent_state.json"))
    if err != nil {
        return nil, fmt.Errorf("failed to load agent_state.json: %w", err)
    }

    var agentState AgentState
    if err := json.Unmarshal(agentStateData, &agentState); err != nil {
        return nil, fmt.Errorf("failed to parse agent_state.json: %w", err)
    }

    // 3. Load messages after checkpoint
    messages, err := loadMessagesAfter(checkpoint.MessageIndex)
    if err != nil {
        return nil, fmt.Errorf("failed to load messages: %w", err)
    }

    // 4. Reconstruct snapshot
    snapshot := &ContextSnapshot{
        LLMContext:     string(llmContext),
        AgentState:     agentState,
        RecentMessages: messages,
    }

    // 5. Validate message index consistency
    if checkpoint.MessageIndex > 0 && len(messages) == 0 {
        // Expected messages after checkpoint but found none
        // This might indicate corruption, but we proceed anyway
        log.Warn("No messages found after checkpoint",
            "checkpoint_message_index", checkpoint.MessageIndex)
    }

    return snapshot, nil
}
```

## Observability

Extensive traceevent logging for debugging:

### Event Schemas

```go
// Trigger event
traceevent.Log(ctx, "context_mgmt_trigger",
    traceevent.Field{Key: "turn", Value: turn},
    traceevent.Field{Key: "mode", Value: "normal|urgent|periodic"},
    traceevent.Field{Key: "tokens_percent", Value: tokensPercent},
    traceevent.Field{Key: "tokens_used", Value: tokensUsed},
    traceevent.Field{Key: "tokens_limit", Value: tokensLimit},
    traceevent.Field{Key: "stale_count", Value: staleCount},
    traceevent.Field{Key: "recent_messages", Value: len(recentMessages)},
    traceevent.Field{Key: "trigger_reason", Value: reason},
    traceevent.Field{Key: "turns_since_last", Value: turnsSinceLast},
)

// Mode switch event
traceevent.Log(ctx, "context_mgmt_mode_switch",
    traceevent.Field{Key: "from", Value: "normal|context_management"},
    traceevent.Field{Key: "to", Value: "context_management|normal"},
    traceevent.Field{Key: "reason", Value: "trigger|complete"},
    traceevent.Field{Key: "user_waiting", Value: true},
)

// Tool call event
traceevent.Log(ctx, "context_mgmt_tool_call",
    traceevent.Field{Key: "tool", Value: "update_llm_context|truncate_messages|no_action"},
    traceevent.Field{Key: "llm_context_chars", Value: len(llmContext)},
    traceevent.Field{Key: "truncate_count", Value: len(truncateIDs)},
    traceevent.Field{Key: "truncate_ids", Value: strings.Join(truncateIDs, ",")},
)

// LLM context update event
traceevent.Log(ctx, "context_mgmt_llm_context_updated",
    traceevent.Field{Key: "chars", Value: len(llmContext)},
    traceevent.Field{Key: "chars_delta", Value: len(new) - len(old)},
    traceevent.Field{Key: "checkpoint_path", Value: checkpointDir},
)

// Message truncate event
traceevent.Log(ctx, "context_mgmt_messages_truncated",
    traceevent.Field{Key: "count", Value: count},
    traceevent.Field{Key: "ids", Value: strings.Join(validIDs, ",")},
    traceevent.Field{Key: "chars_freed", Value: totalCharsFreed},
)

// Checkpoint creation event
traceevent.Log(ctx, "checkpoint_created",
    traceevent.Field{Key: "turn", Value: turn},
    traceevent.Field{Key: "checkpoint_path", Value: checkpointPath},
    traceevent.Field{Key: "message_index", Value: messageIndex},
    traceevent.Field{Key: "llm_context_chars", Value: len(llmContext)},
    traceevent.Field{Key: "recent_messages_count", Value: len(recentMessages)},
)

// Checkpoint load event (resume)
traceevent.Log(ctx, "checkpoint_loaded",
    traceevent.Field{Key: "turn", Value: checkpoint.Turn},
    traceevent.Field{Key: "checkpoint_path", Value: checkpoint.Path},
    traceevent.Field{Key: "message_index", Value: checkpoint.MessageIndex},
    traceevent.Field{Key: "messages_loaded", Value: len(messages)},
)
```

### Debugging with Trace Events

```bash
# Query context management triggers
query 'context_mgmt_trigger' --last 100

# Check trigger frequency
query 'context_mgmt_trigger' --aggregate --field mode --count

# Analyze token trends
query 'context_mgmt_trigger' --format json | jq '.tokens_percent'

# Check what's being truncated
query 'context_mgmt_messages_truncated' --last 50

# Monitor checkpoint creation
query 'checkpoint_created' --last 20

# Debug mode switches
query 'context_mgmt_mode_switch' --last 50
```

## Reused Infrastructure

From the existing codebase:

- ✅ **traceevent**: Observability and debugging (see schema above)
- ✅ **llm protocol**: LLM communication (`pkg/llm/`)
- ✅ **skills**: Skill loading and execution (`pkg/skill/`)
- ✅ **tools**: Tool registry and execution (`pkg/tools/`)
- ✅ **rpc command**: RPC interface (`pkg/rpc/`)
- ✅ **agent loop**: Two-loop structure (`pkg/agent/loop.go`)
  - Outer loop: Turn management
  - Inner loop: Tool execution

## Removed/Replaced Components

The following components are completely removed in this architecture:

- ❌ **ContextManagementTool** (`pkg/tools/context_management.go`)
  - Replaced by mode-specific tool sets

- ❌ **ContextMgmtState** scoring system
  - ProactiveDecisions, ReminderNeeded, ReminderFrequency fields
  - Replaced by simple trigger conditions

- ❌ **Skip mechanism** (skip_turns parameter)
  - Replaced by trigger condition evaluation

- ❌ **Proactive score tracking**
  - No longer needed with event-driven model

- ❌ **`pkg/prompt/context_management.md`**
  - Replaced by mode-specific system prompts

**Note**: The existing `compact.Compactor` may be retained as a fallback or for manual compaction (`/compact` command), but automatic compaction is replaced by the Context Management mode.

## Migration Strategy

**Complete rewrite, not gradual migration.**

This is a **rewrite-level** change, not a refactor. The new architecture has fundamentally different:
- Data flow (event log + snapshot vs linear messages)
- Component boundaries (two-mode operation vs single mode)
- State management (ContextSnapshot vs AgentContext)
- Context control (trigger-driven vs proactive LLM management)

### Coexistence Strategy

During transition period:
- **Old sessions**: Continue using old code (`pkg/context/AgentContext`)
- **New sessions**: Use new architecture (`pkg/context/ContextSnapshot`)
- **Side-by-side**: Both codebases coexist in the same repository
- **Feature flag**: Session-level flag to choose architecture

### Implementation Approach

```
Phase 1: Implement new architecture alongside old code
├── pkg/context/
│   ├── context_snapshot.go       (NEW)
│   ├── trigger.go                 (NEW)
│   └── ...
├── pkg/agent/
│   ├── agent_old.go               (EXISTING, for old sessions)
│   └── agent_new.go               (NEW, for new sessions)

Phase 2: Route sessions based on flag
├── Session starts
├── Check session metadata
│   └── has "use_snapshot_architecture"?
│       ├─ Yes → agent_new.go
│       └─ No  → agent_old.go

Phase 3: After validation, deprecate old code
└── Eventually remove old code entirely
```

### Why Not Gradual Migration?

The architectures are too different to migrate gradually:

| Aspect | Old Architecture | New Architecture |
|--------|-----------------|-------------------|
| State | AgentContext + ContextMgmtState | ContextSnapshot |
| Control | LLM proactive (continuous) | System trigger (event-driven) |
| Context | Linear message history | Structured (LLMContext + RecentMessages) |
| Compaction | compact.Compactor | Context Management mode |
| Tools | Single set, always available | Mode-specific sets |

Mixing these would require complex bridges and adapters, increasing complexity and bugs.

---

## Implementation Details: Common Questions

### Q: How does "user waiting" work in RPC mode?

**A**: The RPC request is **blocked** until context management completes. No intermediate "busy" response is sent.

**Flow**:
```
Client                    Server
  │                          │
  │─── JSON-RPC request ────▶│
  │   {method: "chat"}      │
  │                          │
  │                    ┌─────▶ Check triggers → TRIGGERED!
  │                    │
  │                    │┌────▶ Context Management Mode
  │                    ││
  │                    ││  ├─ Call LLM (5-30s)
  │                    ││  ├─ Update LLMContext
  │                    ││  ├─ Truncate messages
  │                    ││  └─ Create Checkpoint
  │                    ││
  │                    │└─── Back to Normal Mode
  │                    │
  │                    └────▶ Process user message
  │                          │
  │◀── JSON-RPC response ──│
  │   {result: "..."}       │
  │                          │
```

**Client experience**:
- User sends message
- If context management is triggered, the response takes longer (5-30 seconds)
- Client can show "Processing..." spinner
- Client doesn't need to know context management is happening

**Optional**: Add metadata to response
```json
{
  "result": "The file has been updated...",
  "metadata": {
    "context_management_performed": true,
    "context_management_duration_ms": 12345,
    "tokens_before": 45000,
    "tokens_after": 12000
  }
}
```

### Q: What is MessageIndex? Why are checkpoints needed?

**A**: `MessageIndex` is the **line number** in `messages.jsonl` (0-indexed).

**Example**:
```
messages.jsonl:
{"role": "user", "content": "Hello"}           ← Line 0, index = 0
{"role": "assistant", "content": "Hi"}          ← Line 1, index = 1
{"role": "user", "content": "How are you?"}     ← Line 2, index = 2
{"role": "assistant", "content": "Good"}         ← Line 3, index = 3
```

**Why are checkpoints not redundant?**

| Data | messages.jsonl | Checkpoint | Purpose |
|------|---------------|------------|---------|
| Complete conversation history | ✅ Full | ❌ None | Audit, replay, debugging |
| LLM-maintained structured context | ❌ None | ✅ llm_context.txt | Fast recovery, LLM input |
| System metadata | ❌ None | ✅ agent_state.json | State tracking |

**Checkpoint value**:
1. **LLMContext cannot be reconstructed** from messages.jsonl - it's generated by LLM
2. **Fast recovery**: Read 2 files vs scan thousands of lines
3. **Summarized understanding**: Provides LLM's view of conversation at that point

**Analogy**:
```
messages.jsonl = Complete raw footage (all data)
Checkpoint = Annotated index (summary and key points)

You can use index to find footage, but can't recreate index from footage
```

### Q: When is stale calculated? Dynamic or stored?

**A**: `stale` is **dynamically calculated** when rendering messages for LLM.

**Calculation timing**:
```go
func (m AgentMessage) RenderForLLM(allToolResults []AgentMessage) string {
    // Calculate position in visible tool results
    position := findPosition(m, allToolResults)

    // Calculate stale dynamically
    stale := len(allToolResults) - position - 1

    // Render with stale value
    return fmt.Sprintf(`<agent:tool ... stale="%d" ...>`, stale)
}
```

**Scope of totalVisibleToolResults**:
- Only `toolResult` type messages in `RecentMessages`
- Excludes `Truncated` messages
- Excludes non-tool-result messages

```go
func getAllVisibleToolResults(snapshot *ContextSnapshot) []AgentMessage {
    var results []AgentMessage
    for _, msg := range snapshot.RecentMessages {
        if msg.Role == "toolResult" && !msg.IsTruncated() {
            results = append(results, msg)
        }
    }
    return results
}
```

**Why dynamic calculation?**
- `stale` is relative to "what's currently visible"
- After each context management, visible set changes
- Dynamic calculation ensures correctness

### Q: Mode-specific prompts: Hardcoded or file-loaded?

**A**: **Hardcoded initially**, file-loading can be added later.

**Initial implementation**:
```go
// pkg/prompt/builder.go
package prompt

const (
    NormalSystemPrompt = `You are a helpful coding assistant.
The system will automatically manage context size.
`

    ContextMgmtSystemPrompt = `<system mode="context_management">
You are in CONTEXT MANAGEMENT MODE...
`
)

func BuildSystemPrompt(mode AgentMode) string {
    switch mode {
    case ModeNormal:
        return NormalSystemPrompt
    case ModeContextMgmt:
        return ContextMgmtSystemPrompt
    default:
        return NormalSystemPrompt
    }
}
```

**Optional enhancement** (after system is stable):
```go
// Support user-customizable prompts
func LoadSystemPrompt(mode AgentMode) string {
    // Try custom file first
    filename := fmt.Sprintf("%s_prompt.md", mode)
    if custom, err := os.ReadFile(filename); err == nil {
        return string(custom)
    }
    // Fallback to default
    return GetDefaultSystemPrompt(mode)
}
```

**Recommendation**: Start hardcoded, add file-loading only if there's strong user demand.

### Q: Where does TriggerChecker go?

**A**: In **`pkg/context/`** package.

**Recommended structure**:
```
pkg/context/
├── context_snapshot.go       // ContextSnapshot, AgentState definitions
├── trigger.go                 // TriggerChecker implementation
├── trigger_config.go          // Trigger configuration constants
├── checkpoint.go               // Checkpoint I/O operations
├── checkpoint_index.go        // Checkpoint index management
├── llm_context.go             // LLMContext file I/O
├── message.go                 // Message rendering, truncation marks
└── mode.go                    // AgentMode type and utilities
```

**Configuration approach**:
```go
// pkg/context/trigger_config.go
package context

const (
    // Trigger conditions
    IntervalTurns      = 10
    MinTurns           = 5
    TokenThreshold     = 0.40
    TokenUrgent        = 0.75
    StaleCount         = 15
    MinInterval        = 3

    // Message management
    RecentMessagesKeep       = 30
    RecentMessagesShowInMgmt  = 10

    // Tool output formatting
    ToolOutputMaxChars     = 2000
    ToolOutputPreviewHead  = 1800
    ToolOutputPreviewTail  = 200

    // Checkpoint
    CheckpointDirPattern  = "checkpoint_%05d"
    CurrentLinkName       = "current"
)

// TriggerChecker can be instantiated with custom values (for testing)
type TriggerChecker struct {
    IntervalTurns      int
    MinTurns           int
    TokenThreshold     float64
    TokenUrgent        float64
    StaleCount         int
    MinInterval        int
}

func NewTriggerChecker() *TriggerChecker {
    return &TriggerChecker{
        IntervalTurns:      IntervalTurns,
        MinTurns:           MinTurns,
        TokenThreshold:     TokenThreshold,
        TokenUrgent:        TokenUrgent,
        StaleCount:         StaleCount,
        MinInterval:        MinInterval,
    }
}

func NewTestTriggerChecker(custom TriggerConfig) *TriggerChecker {
    return &TriggerChecker{
        IntervalTurns:      custom.IntervalTurns,
        // ...
    }
}
```

**Future: Runtime configuration** (optional, after stable):
```go
// Support environment variable overrides
func (t *TriggerChecker) LoadFromEnv() {
    if v := os.Getenv("CONTEXT_MGMT_INTERVAL"); v != "" {
        if i, err := strconv.Atoi(v); err == nil {
            t.IntervalTurns = i
        }
    }
    if v := os.Getenv("CONTEXT_MGMT_TOKEN_THRESHOLD"); v != "" {
        if f, err := strconv.ParseFloat(v, 64); err == nil {
            t.TokenThreshold = f
        }
    }
    // ...
}
```

**Recommendation**: Start with hardcoded constants for simplicity and predictability. Add runtime configuration only after the system is proven stable through testing.

## Implementation Plan

### Phase 1: Core Structures (Week 1-2)
- [ ] `ContextSnapshot` and its components
- [ ] `AgentState` definition
- [ ] Message truncation marking
- [ ] `TriggerChecker` implementation
- [ ] Checkpoint I/O operations
- [ ] `checkpoint_index.json` management

### Phase 2: Two-Mode Operation (Week 2-3)
- [ ] Mode switching logic
- [ ] Normal mode flow
- [ ] Context Management mode flow
- [ ] User wait mechanism
- [ ] Trigger condition checking
- [ ] `minInterval` enforcement (except urgent)

### Phase 3: Tools and Prompts (Week 3-4)
- [ ] Context management tools implementation
- [ ] System prompts for both modes
- [ ] Tool output formatting with `<agent:tool>` tags
- [ ] Stale calculation
- [ ] Large file preview

### Phase 4: Integration (Week 4-5)
- [ ] Integrate with agent loop
- [ ] Mode switching in turn processing
- [ ] Resume support
- [ ] RPC integration for manual triggers
- [ ] Checkpoint creation on context mgmt completion

### Phase 5: Observability (Week 5-6)
- [ ] Traceevent logging for all key events
- [ ] Debug queries and tools
- [ ] Performance monitoring
- [ ] Error handling and recovery

## Configuration

Initial implementation uses hardcoded values for simplicity:

```go
const (
    // Trigger conditions
    IntervalTurns      = 10
    MinTurns           = 5
    TokenThreshold     = 0.40
    TokenUrgent        = 0.75
    StaleCount         = 15
    MinInterval        = 3

    // Message management
    RecentMessagesKeep       = 30  // After context mgmt
    RecentMessagesShowInMgmt  = 10  // Shown to LLM during mgmt

    // Tool output formatting
    ToolOutputMaxChars     = 2000
    ToolOutputPreviewHead  = 1800
    ToolOutputPreviewTail  = 200

    // Checkpoint
    CheckpointDirPattern  = "checkpoint_%05d"
    CurrentLinkName       = "current"
)
```

After the system is stable, configuration can be added:
- Config file support (YAML/JSON)
- Environment variable overrides
- Per-session configuration
- Runtime reconfiguration

## Future Considerations

### Checkpoint Cleanup

Checkpoints accumulate over time. Use external scripts for cleanup:

```bash
# scripts/clean-checkpoints.sh
# Remove checkpoints older than N days
# Keep every Nth checkpoint for long-term history

# Examples:
# ./scripts/clean-checkpoints.sh --older-than 7d --keep-every 5
# ./scripts/clean-checkpoints.sh --keep-last 10
```

### Configuration

After the system is stable, consider adding:
- Config file support
- Per-session override capability
- Dynamic threshold adjustment based on model performance
- A/B testing of trigger conditions

### Advanced Features

- **Selective checkpoint**: Only checkpoint certain message types
- **Incremental checkpoint**: Store diff from previous checkpoint
- **Merge nearby checkpoints**: Combine close checkpoints to save space
- **Compression**: Compress old checkpoint data
- **Checkpoint validation**: Verify checkpoint integrity on load

## Appendix: Key Data Structures

```go
// AgentMode represents the current operating mode
type AgentMode string

const (
    ModeNormal        AgentMode = "normal"
    ModeContextMgmt   AgentMode = "context_management"
)

// ContextSnapshot is the in-memory representation of conversation state
type ContextSnapshot struct {
    LLMContext     string
    RecentMessages []AgentMessage
    AgentState     AgentState
}

// AgentMessage represents a single message
type AgentMessage struct {
    Role      string           // "user", "assistant", "toolResult"
    Content   []ContentBlock   // Message content
    ToolCallID string           // For tool results
    ToolName  string           // For tool results
    Stale     int              // Calculated staleness score

    // Truncation tracking
    Truncated     bool   `json:"truncated,omitempty"`
    TruncatedAt   int    `json:"truncated_at,omitempty"`
    OriginalSize  int    `json:"original_size,omitempty"`

    // Visibility control
    AgentVisible  bool   `json:"agent_visible"`
    UserVisible   bool   `json:"user_visible"`
}

// ContentBlock represents a block of content
type ContentBlock interface {
    Type() string
}

type TextContent struct {
    Type string
    Text string
}

type ToolCallContent struct {
    Type     string
    ID       string
    Name     string
    Arguments map[string]any
}

// CheckpointInfo represents a checkpoint in the index
type CheckpointInfo struct {
    Turn              int    `json:"turn"`
    MessageIndex      int    `json:"message_index"`
    Path              string `json:"path"`
    CreatedAt         string `json:"created_at"`
    LLMContextChars   int    `json:"llm_context_chars,omitempty"`
    RecentMessagesCount int   `json:"recent_messages_count,omitempty"`
}
```

# Context Management Test Cases (TDD Approach)

## TDD Philosophy Review

From your skills:
- **Test real behavior, not mock behavior**
- **No test-only methods in production code**
- **Mock at the right level, after understanding dependencies**
- **Complete mocks that mirror real API**
- **Write test first, watch it fail, then implement**

## Approach: Behavior-First Test Design

Instead of testing implementation details, we design tests around **observable behaviors** from the design document.

---

## Test Case Categories

### Category 1: Event Sourcing - Journal Replay

**Why first**: This is the foundation. If replay is wrong, everything is wrong.

#### Test 1.1: Empty Journal Produces Base Snapshot
```go
func TestEmptyJournal_Replay_ReturnsBaseSnapshot(t *testing.T) {
    // Given: A checkpoint with LLMContext and some messages
    checkpoint := &Checkpoint{
        Turn:         10,
        MessageIndex: 20,
        LLMContext:   "Initial context",
        BaseMessages: []AgentMessage{
            createMessage("user", "hello"),
            createMessage("assistant", "hi"),
        },
    }

    // When: Replaying empty journal
    journal := []*JournalEntry{}
    snapshot := BuildContextSnapshot(checkpoint, journal)

    // Then: Snapshot contains only base messages
    assert.Equal(t, checkpoint.LLMContext, snapshot.LLMContext)
    assert.Equal(t, 2, len(snapshot.RecentMessages))
}
```

**Behavior**: Starting from checkpoint, no events → base state

#### Test 1.2: Message Events Append to Snapshot
```go
func TestMessageEvents_Replay_AppendMessages(t *testing.T) {
    checkpoint := createBaseCheckpoint()

    journal := []*JournalEntry{
        {Type: "message", Message: createMessage("user", "msg1")},
        {Type: "message", Message: createMessage("assistant", "resp1")},
    }

    snapshot := BuildContextSnapshot(checkpoint, journal)

    // Base messages + journal messages
    assert.Equal(t, 4, len(snapshot.RecentMessages)) // 2 base + 2 new
    assert.Equal(t, "msg1", snapshot.RecentMessages[2].Content)
    assert.Equal(t, "resp1", snapshot.RecentMessages[3].Content)
}
```

**Behavior**: New messages appear after replay

#### Test 1.3: Truncate Events Mark Messages Truncated
```go
func TestTruncateEvent_Replay_MarksMessageTruncated(t *testing.T) {
    checkpoint := createBaseCheckpoint()

    journal := []*JournalEntry{
        {Type: "message", Message: createToolResult("call_1", "big output")},
        {Type: "truncate", Truncate: &TruncateEvent{
            ToolCallID: "call_1",
            Turn:       5,
            Trigger:    "context_management",
        }},
    }

    snapshot := BuildContextSnapshot(checkpoint, journal)

    msg := findToolResult(snapshot.RecentMessages, "call_1")
    assert.True(t, msg.Truncated)
    assert.Equal(t, "big output", msg.Content) // Original preserved
}
```

**Behavior**: Truncate events mark messages, don't replace content

#### Test 1.4: Replay is Deterministic
```go
func TestReplay_Deterministic_SameResult(t *testing.T) {
    checkpoint := createBaseCheckpoint()
    journal := generateRandomJournal(100)

    snapshot1 := BuildContextSnapshot(checkpoint, journal)
    snapshot2 := BuildContextSnapshot(checkpoint, journal)

    assert.Equal(t, snapshot1, snapshot2)
}
```

**Behavior**: Same journal → same snapshot (idempotence)

---

### Category 2: Trigger Conditions

**Why second**: Triggers drive the entire system. Must be correct.

#### Test 2.1: Urgent Mode Ignores MinInterval
```go
func TestTrigger_UrgentTokens_IgnoresMinInterval(t *testing.T) {
    // Given: 80% tokens, only 1 turn since last trigger
    snapshot := createSnapshot(
        withTokensPercent(0.80),
        withTurnsSinceLast(1), // < MinInterval(3)
    )

    checker := NewTriggerChecker()

    // When: Checking trigger
    triggered, urgency := checker.ShouldTrigger(snapshot)

    // Then: Should trigger despite minInterval
    assert.True(t, triggered)
    assert.Equal(t, "urgent", urgency)
}
```

**Behavior**: Critical token usage overrides minInterval

#### Test 2.2: Normal Trigger Respects MinInterval
```go
func TestTrigger_NormalTokens_BlockedByMinInterval(t *testing.T) {
    // Given: 50% tokens, only 2 turns since last
    snapshot := createSnapshot(
        withTokensPercent(0.50),
        withTurnsSinceLast(2), // < MinInterval(3)
    )

    triggered, _ := NewTriggerChecker().ShouldTrigger(snapshot)

    // Then: Should NOT trigger (blocked by minInterval)
    assert.False(t, triggered)
}
```

**Behavior**: MinInterval prevents excessive triggers

#### Test 2.3: Skip Condition When Healthy
```go
func TestTrigger_HealthyContext_Skips(t *testing.T) {
    // Given: 25% tokens, 25 turns, 10 turns since last
    snapshot := createSnapshot(
        withTokensPercent(0.25), // < 30%
        withTotalTurns(25),      // >= 20
        withTurnsSinceLast(10),
    )

    triggered, _ := NewTriggerChecker().ShouldTrigger(snapshot)

    // Then: Skip (context is healthy)
    assert.False(t, triggered)
}
```

**Behavior**: Don't trigger when context is healthy

#### Test 2.4: Periodic Check
```go
func TestTrigger_PeriodicTurn_Triggers(t *testing.T) {
    // Given: Turn 20 (multiple of 10), low tokens
    snapshot := createSnapshot(
        withTokensPercent(0.15),
        withTotalTurns(20), // 20 % 10 == 0
    )

    triggered, urgency := NewTriggerChecker().ShouldTrigger(snapshot)

    // Then: Trigger for periodic check
    assert.True(t, triggered)
    assert.Equal(t, "periodic", urgency)
}
```

**Behavior:**
 Periodic maintenance triggers

---

### Category 3: Mode-Specific Rendering

**Why third**: Critical for cache-friendly LLM requests.

#### Test 3.1: Normal Mode Hides Tool Call ID
```go
func TestRender_NormalMode_ToolCallIDHidden(t *testing.T) {
    msg := createToolResult("call_abc", "output")

    rendered := RenderToolResult(msg, ModeNormal, 5)

    // Then: No tool_call_id visible
    assert.NotContains(t, rendered, "call_abc")
    assert.NotContains(t, rendered, "<agent:tool")
    assert.Equal(t, "output", rendered)
}
```

**Behavior**: LLM in normal mode never sees tool_call_id

#### Test 3.2: ContextMgmtMode Exposes Tool Call ID
```go
func TestRender_ContextMgmtMode_ToolCallIDVisible(t *testing.T) {
    msg := createToolResult("call_abc", "output")

    rendered := RenderToolResult(msg, ModeContextMgmt, 5)

    // Then: Metadata present in XML format
    assert.Contains(t, rendered, `id="call_abc"`)
    assert.Contains(t, rendered, `stale="5"`)
    assert.Contains(t, rendered, `chars="6"`)
    assert.Contains(t, rendered, "output")
}
```

**Behavior**: LLM in context mgmt mode can see ID to truncate

#### Test 3.3: LLM Request Structure is Cache-Friendly
```go
func TestBuildLLMRequest_LLMContextNotInSystemPrompt(t *testing.T) {
    snapshot := &ContextSnapshot{
        LLMContext: "Task: Implement X",
        RecentMessages: []AgentMessage{
            createMessage("user", "do task"),
        },
    }

    request := buildLLMRequest(snapshot, ModeNormal)

    // Then: System prompt is clean (no LLMContext)
    assert.NotContains(t, request.SystemPrompt, "Implement X")

    // But LLMContext is in user messages before last user message
    llmContextMsg := findLLMContextMessage(request.Messages)
    assert.Equal(t, "user", llmContextMsg.Role)
    assert.Contains(t, llmContextMsg.Content, "<agent:llm_context>")
    assert.Contains(t, llmContextMsg.Content, "Implement X")
}
```

**Behavior**: System prompt stable → better caching

---

### Category 4: Checkpoint Persistence

**Why fourth**: State must survive process restart.

#### Test 4.1: Save and Load Produces Same State
```go
func TestCheckpoint_SaveLoad_PreservesState(t *testing.T) {
    tempDir := t.TempDir()

    // Given: A checkpoint
    original := &Checkpoint{
        Turn:       15,
        LLMContext: "Current task",
        AgentState: AgentState{
            TotalTurns: 15,
            TokensUsed: 5000,
        },
    }

    // When: Save and load
    path := filepath.Join(tempDir, "checkpoint_00015")
    err := SaveCheckpoint(path, original)
    require.NoError(t, err)

    loaded, err := LoadCheckpoint(path)
    require.NoError(t, err)

    // Then: Same state
    assert.Equal(t, original.Turn, loaded.Turn)
    assert.Equal(t, original.LLMContext, loaded.LLMContext)
    assert.Equal(t, original.AgentState.TotalTurns, loaded.AgentState.TotalTurns)
}
```

**Behavior**: Checkpoint preserves exact state

#### Test 4.2: current/ Symlink Points to Latest
```go
func TestCurrentSymlink_PointsToLatest(t *testing.T) {
    tempDir := t.TempDir()

    // Create two checkpoints
    cp1 := createCheckpointAt(tempDir, 10)
    cp2 := createCheckpointAt(tempDir, 20)

    // Update current/ to latest
    updateCurrentLink(tempDir, cp2)

    // Verify: current/ points to checkpoint_00020
    target, err := os.Readlink(filepath.Join(tempDir, "current"))
    require.NoError(t, err)
    assert.Contains(t, target, "checkpoint_00020")
}
```

**Behavior**: current/ always points to latest checkpoint

---

### Category 5: Context Management Flow

**Why fifth**: End-to-end behavior of the core feature.

#### Test 5.1: No Action Updates LastTriggerTurn
```go
func TestContextMgmt_NoAction_UpdatesLastTriggerTurn(t *testing.T) {
    session := createTestSession()
    session.AgentState.LastTriggerTurn = 0
    session.AgentState.TurnsSinceLastTrigger = 10

    // Execute context mgmt, LLM chooses no_action
    result := &MgmtResult{Action: "no_action"}
    err := ExecuteContextMgmt(session, result)

    // Then: LastTriggerTurn updated, no checkpoint created
    assert.NoError(t, err)
    assert.Equal(t, 10, session.AgentState.LastTriggerTurn)
    assert.False(t, checkpointExists(session))

    // Next trigger respects minInterval
    triggered, _ := checkTrigger(session)
    assert.False(t, triggered) // Blocked by minInterval
}
```

**Behavior**: no_action "consumes" trigger without checkpoint

#### Test 5.2: Truncate Action Records Event to Log
```go
func TestContextMgmt_Truncate_WritesEventToLog(t *testing.T) {
    session := createTestSession()
    session.AddToolResult("call_1", "big output")

    // Execute context mgmt, LLM truncates call_1
    result := &MgmtResult{
        Action:         "truncate",
        TruncatedIDs:   []string{"call_1"},
    }
    err := ExecuteContextMgmt(session, result)

    // Then: Truncate event in journal
    journal := session.GetJournal()
    assert.Contains(t, journal, `"type":"truncate"`)
    assert.Contains(t, journal, `"tool_call_id":"call_1"`)

    // And message marked truncated
    msg := findToolResult(session.GetMessages(), "call_1")
    assert.True(t, msg.Truncated)
}
```

**Behavior**: Truncate operations are logged and applied

#### Test 5.3: Update LLMContext Creates Checkpoint
```go
func TestContextMgmt_UpdateContext_CreatesCheckpoint(t *testing.T) {
    session := createTestSession()

    result := &MgmtResult{
        Action:       "update_context",
        NewLLMContext: "New summary of task",
    }
    err := ExecuteContextMgmt(session, result)

    // Then: Checkpoint created with new context
    assert.NoError(t, err)
    assert.True(t, checkpointExists(session))

    // Verify checkpoint content
    checkpoint := loadLatestCheckpoint(session)
    assert.Equal(t, "New summary of task", checkpoint.LLMContext)
}
```

**Behavior**: State changes create checkpoints

---

### Category 6: Session Operations

**Why sixth**: User-visible features.

#### Test 6.1: Resume Loads from Checkpoint
```go
func TestResume_LoadsFromCheckpoint(t *testing.T) {
    tempDir := t.TempDir()

    // Create session with history
    session := createSessionAt(tempDir, "session_abc")
    session.AddMessage("user", "task 1")
    session.AddMessage("assistant", "done 1")
    session.CreateCheckpoint() // Turn 2

    session.AddMessage("user", "task 2")

    // When: Resuming (loads latest)
    resumed, err := LoadSession(tempDir, "")
    require.NoError(t, err)

    // Then: State from checkpoint + new messages
    assert.Equal(t, "task 1", resumed.GetMessages()[0].Content)
    assert.Equal(t, "task 2", resumed.GetMessages()[2].Content)
}
```

**Behavior**: Resume reconstructs state from checkpoint + journal

#### Test 6.2: Fork Creates Independent Session
```go
func TestFork_CreatesIndependentHistory(t *testing.T) {
    tempDir := t.TempDir()

    original := createSessionAt(tempDir, "original")
    original.AddMessage("user", "original message")

    // Fork at current turn
    forked, err := ForkSession(tempDir, "forked", original.CurrentTurn())
    require.NoError(t, err)

    // Verify: Different session IDs
    assert.NotEqual(t, original.ID(), forked.ID())

    // Verify: Independent history
    original.AddMessage("user", "only in original")
    forked.AddMessage("user", "only in forked")

    assert.NotContains(t, original.GetContent(), "only in forked")
    assert.NotContains(t, forked.GetContent(), "only in original")
}
```

**Behavior**: Forked session has independent message log

#### Test 6.3: Rewind Only Goes Backward
```go
func TestRewind_OnlyBackward(t *testing.T) {
    tempDir := t.TempDir()

    session := createSessionWithTurns(tempDir, 30) // 30 turns

    // Rewind to turn 15
    rewound, err := RewindSession(tempDir, "rewound", 15)
    require.NoError(t, err)

    // Verify: Only 15 turns
    assert.Equal(t, 15, rewound.TurnCount())

    // Try to "rewind" to future (turn 25) - should fail
    _, err = RewindSession(tempDir, "future", 25)
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "can only rewind to past")
}
```

**Behavior**: Rewind cannot go to future (that would be resume)

---

## Test Organization: From Simple to Complex

### Phase 1: Core Data Structures (Week 1)
1. Empty journal replay
2. Message events append
3. Truncate events mark
4. Replay determinism

### Phase 2: Trigger Logic (Week 1)
5. Urgent ignores minInterval
6. Normal respects minInterval
7. Skip when healthy
8. Periodic triggers

### Phase 3: Rendering (Week 2)
9. Normal mode hides ID
10. ContextMgmt mode exposes ID
11. Cache-friendly LLM request

### Phase 4: Persistence (Week 2)
12. Checkpoint save/load
13. current/ symlink
14. Index management

### Phase 5: Context Mgmt Flow (Week 3)
15. no_action behavior
16. truncate writes event
17. update creates checkpoint

### Phase 6: Session Operations (Week 3-4)
18. Resume from checkpoint
19. Fork independence
20. Rewind backward only

---

## Anti-Pattern Avoidance

### ❌ Don't Test Mock Behavior

**Bad**:
```go
func TestTrigger_MockChecker(t *testing.T) {
    mockChecker := &MockTriggerChecker{}
    // Testing mock, not real behavior
}
```

**Good**:
```go
func TestTrigger_UrgentTokens_IgnoresMinInterval(t *testing.T) {
    // Use real TriggerChecker
    // Test real trigger logic
}
```

### ❌ Don't Add Test-Only Methods

**Bad**:
```go
func (s *Session) Destroy() { // Only for cleanup
    os.RemoveAll(s.path)
}
```

**Good**:
```go
// In test utils
func cleanupSession(s *Session) {
    os.RemoveAll(s.path)
}
```

### ❌ Don't Mock Without Understanding

**Bad**:
```go
func TestCheckpoint_Save_IgnoreIO(t *testing.T) {
    // Mock filesystem - but we don't know what's needed yet
}
```

**Good**:
```go
func TestCheckpoint_SaveLoad_PreservesState(t *testing.T) {
    // Use real temp dir (t.TempDir())
    // Test real save/load behavior
}
```

---

## Next Steps: TDD Implementation

For each test case:

1. **Write test first** (watch it fail - compilation error is fine)
2. **Implement minimal code** to make it pass
3. **Refactor** while keeping tests green

Example sequence for Event Replay:

```
1. Test 1.1: Empty Journal (write, fails to compile)
   → Implement: BuildContextSnapshot() skeleton
   → Implement: JournalEntry, Checkpoint structs

2. Test 1.2: Message Events (write, fails)
   → Implement: Message event replay logic

3. Test 1.3: Truncate Events (write, fails)
   → Implement: Truncate event replay logic
   → Implement: markTruncated()

4. Test 1.4: Determinism (write, should pass)
   → Refactor: Optimize if needed

... continue with next category
```

---

## Summary

| Category | Focus | Test Count |
|----------|-------|------------|
| Event Replay | Foundation | 4 |
| Trigger Conditions | Decision logic | 4 |
| Rendering | LLM interaction | 3 |
| Persistence | State survival | 2 |
| Context Mgmt | Core flow | 3 |
| Session Ops | User features | 3 |

**Total**: 19 core test cases, each testing **real behavior**, not implementation details.

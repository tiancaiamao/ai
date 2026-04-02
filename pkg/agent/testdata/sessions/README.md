# Session-Based Testing

This directory contains real session data for testing agent behavior.

## Quick Start: Creating a New Test Case

When you encounter a bug, you can quickly create a test case from the actual session:

### 1. Copy the Session Data

```bash
# Copy your session to testdata
SESSION_ID="ed7fbccd-5f01-42da-ba0a-a0b7023214bc"
SESSION_DIR="$HOME/.ai/sessions/--Users-genius-project-ai--/$SESSION_ID"

mkdir -p pkg/agent/testdata/sessions/my_bug_case
cp -r "$SESSION_DIR/checkpoints" pkg/agent/testdata/sessions/my_bug_case/
cp "$SESSION_DIR/checkpoint_index.json" pkg/agent/testdata/sessions/my_bug_case/
cp "$SESSION_DIR/messages.jsonl" pkg/agent/testdata/sessions/my_bug_case/

# Optional: truncate messages.jsonl to reduce test size
head -100 "$SESSION_DIR/messages.jsonl" > pkg/agent/testdata/sessions/my_bug_case/messages.jsonl
```

### 2. Write the Test Case

```go
func TestSession_MyBugCase(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping test with real session data in short mode")
    }

    helper := NewSessionTest(t, SessionTestCase{
        Name:        "my_bug_case",
        SessionDir:  "my_bug_case",
        Description: "Tests that resume correctly handles X",
        ExpectedResults: struct {
            MinMessageCount   int
            MaxMessageCount   int
            FirstMessageRole  string
            LLMContextNotEmpty bool
            CheckpointHadMessages *bool
        }{
            MinMessageCount:    10,  // Adjust based on your data
            LLMContextNotEmpty: true,
        },
    })
    defer helper.Cleanup()

    helper.LoadSession()

    // Add your custom test logic here
    snapshot := helper.GetSnapshot()
    // ... do something with snapshot ...

    // Verify results
    helper.VerifyMinMessages(10).
        VerifyLLMContextNotEmpty()
}
```

### 3. Run the Test

```bash
go test ./pkg/agent -v -run TestSession_MyBugCase
```

## Available Test Helpers

| Method | Description |
|--------|-------------|
| `LoadSession()` | Loads checkpoint and journal from session |
| `VerifyMinMessages(n)` | Verifies at least n messages |
| `VerifyMaxMessages(n)` | Verifies at most n messages |
| `VerifyFirstMessageRole(role)` | Verifies first message has expected role |
| `VerifyLLMContextNotEmpty()` | Verifies LLMContext was loaded |
| `GetSnapshot()` | Returns snapshot for custom assertions |
| `GetCheckpoint()` | Returns checkpoint info |
| `GetJournalEntries()` | Returns journal entries |
| `Cleanup()` | Cleans up resources |

## Example Test Cases

### Test 1: Resume with Missing Messages

```go
helper := NewSessionTest(t, SessionTestCase{
    Name:        "resume_no_messages",
    SessionDir:  "resume_bug_case",
    Description: "Tests resume when checkpoint has no messages.jsonl",
})
defer helper.Cleanup()
helper.LoadSession()

// Verify we recovered messages from journal
snapshot := helper.GetSnapshot()
assert.Greater(t, len(snapshot.RecentMessages), 0,
    "Should have recovered messages from journal")
```

### Test 2: Context Management Trigger

```go
helper := NewSessionTest(t, SessionTestCase{
    Name:        "context_mgmt_trigger",
    SessionDir:  "trigger_bug_case",
    Description: "Tests that context management triggers at correct token level",
})
defer helper.Cleanup()
helper.LoadSession()

snapshot := helper.GetSnapshot()
tokenPercent := snapshot.EstimateTokenPercent()

assert.True(t, tokenPercent > 0.4,
    "Token usage should be above 40%% to trigger")
```

### Test 3: Compact Event in Journal

```go
helper := NewSessionTest(t, SessionTestCase{
    Name:        "compact_event",
    SessionDir:  "compact_case",
    Description: "Tests that compact events are properly recorded",
})
defer helper.Cleanup()
helper.LoadSession()

entries := helper.GetJournalEntries()
foundCompact := false
for _, e := range entries {
    if e.Type == "compact" {
        foundCompact = true
        assert.NotEmpty(t, e.Compact.Summary,
            "Compact event should have summary")
    }
}
assert.True(t, foundCompact, "Should have compact event in journal")
```

## Test Data Organization

```
testdata/sessions/
├── README.md              (this file)
├── resume_bug_case/       (example: checkpoint without messages.jsonl)
│   ├── checkpoint_index.json
│   ├── checkpoints/
│   │   └── checkpoint_00008/
│   │       ├── agent_state.json
│   │       └── llm_context.txt
│   └── messages.jsonl
└── your_bug_case/         (add your cases here)
    ├── checkpoint_index.json
    ├── checkpoints/
    └── messages.jsonl
```

## Best Practices

1. **Keep test data small**: Truncate `messages.jsonl` to first 100-200 lines
2. **Use descriptive names**: Name directories after the bug they test
3. **Document the bug**: Add a clear `Description` to each test case
4. **Clean up**: Always `defer helper.Cleanup()`
5. **Run in short mode**: Use `if testing.Short() { t.Skip() }` for slow tests

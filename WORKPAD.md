## Workpad

```
geniusdeMBP:/Users/genius/.symphony/workspaces/f0b5150d-ab81-4903-856c-6669e501f8b6@4bba059
```

### Problem

Checkpoint only saves `llm_context.txt` and `agent_state.json`, not `RecentMessages`. When resuming:
- `LoadCheckpoint` returns empty `RecentMessages`
- `ReconstructSnapshotWithCheckpoint` sees empty RecentMessages and replays from index 0
- With many journal entries, resume is very slow

### Root Cause Analysis

1. **`SaveCheckpoint`** (`pkg/context/checkpoint_io.go:11`): Explicitly skips saving messages (line 47 comment: "DO NOT save messages.jsonl")
2. **`LoadCheckpoint`** (`pkg/context/checkpoint_io.go:70`): Returns `RecentMessages: []AgentMessage{}` (empty)
3. **`ReconstructSnapshotWithCheckpoint`** (`pkg/context/reconstruction.go:27`): Checks `len(snapshot.RecentMessages) > 0` to decide replay start - always falls through to `startIndex = 0`
4. Existing design tests (`checkpoint_design_test.go`) enforce NO messages.jsonl in checkpoint

### Plan

- [ ] 1. Modify `SaveCheckpoint` to persist RecentMessages to `messages.jsonl` in checkpoint dir
  - [ ] 1.1 Use existing `saveMessagesJSONL` helper
  - [ ] 1.2 Update `CheckpointInfo` to record message count for diagnostics
- [ ] 2. Modify `LoadCheckpoint` to load RecentMessages from `messages.jsonl` if present
  - [ ] 2.1 Use existing `loadMessagesJSONL` helper
  - [ ] 2.2 Backward compatible: if no `messages.jsonl`, return empty (old checkpoints still work)
- [ ] 3. Verify `ReconstructSnapshotWithCheckpoint` incremental replay logic works correctly
  - [ ] 3.1 When RecentMessages loaded from checkpoint → replay only增量 entries after `checkpoint.MessageIndex`
  - [ ] 3.2 When no RecentMessages (old checkpoint) → still replay from 0 (backward compat)
- [ ] 4. Update existing design tests
  - [ ] 4.1 Fix `TestCheckpoint_ShouldNotSaveMessagesJsonl` → checkpoint SHOULD save messages.jsonl
  - [ ] 4.2 Fix `TestCheckpoint_LoadWithoutMessagesJsonl` → verify RecentMessages is preserved
  - [ ] 4.3 Update `TestResume_FromCheckpointWithJournal` to verify incremental replay
- [ ] 5. Add new tests for the fix
  - [ ] 5.1 Test SaveCheckpoint persists RecentMessages to messages.jsonl
  - [ ] 5.2 Test LoadCheckpoint restores RecentMessages from messages.jsonl
  - [ ] 5.3 Test incremental replay (only replays entries after checkpoint's MessageIndex)
  - [ ] 5.4 Test backward compatibility (old checkpoint without messages.jsonl still works)
- [ ] 6. Run all tests to verify no regressions

### Acceptance Criteria

- [ ] `SaveCheckpoint` writes `messages.jsonl` into checkpoint dir when RecentMessages is non-empty
- [ ] `LoadCheckpoint` reads `messages.jsonl` and populates `RecentMessages`
- [ ] `ReconstructSnapshotWithCheckpoint` only replays增量 journal entries after checkpoint's `MessageIndex`
- [ ] Old checkpoints without `messages.jsonl` still load correctly (backward compatible)
- [ ] All existing tests pass
- [ ] New tests pass

### Validation

- [ ] targeted tests: `go test ./pkg/context/... -v -run Checkpoint`
- [ ] targeted tests: `go test ./pkg/context/... -v -run Reconstruct`
- [ ] targeted tests: `go test ./pkg/agent/... -v -run Checkpoint`
- [ ] broader tests: `go test ./pkg/context/... -v`
- [ ] broader tests: `go test ./pkg/agent/... -v`

### Notes

- The codebase already has `saveMessagesJSONL` and `loadMessagesJSONL` helper functions in `checkpoint_io.go` — they just aren't being called
- The existing design test `TestCheckpoint_ShouldNotSaveMessagesJsonl` explicitly asserts NO messages.jsonl — this test's intent was wrong per the task description and must be updated
- The `CheckpointInfo` already has `RecentMessagesCount` field (just used for diagnostics)
## Workpad

```
tiancaiamao/ai:WORKPAD @ e211ba7f-3e3d-4388-8395-0a25ac9fb394
```

### Plan

- [ ] 1. Understand the race condition fix for tool call pairing
  - [ ] 1.1 Review the `ensureToolCallPairing` function in compact.go
  - [ ] 1.2 Review orphan tool call detection in conversion_visibility_test.go
- [ ] 2. Run existing tests to verify the fix
  - [ ] 2.1 Run `pkg/compact` tests
  - [ ] 2.2 Run `pkg/agent` conversion tests
- [ ] 3. Verify agent runs without orphaned tool calls
  - [ ] 3.1 Run `TestConvertMessagesToLLMDropsOrphanedToolResult`
  - [ ] 3.2 Run `TestConvertMessagesToLLMRetainsResolvedToolCallsWhenPartiallyMatched`
  - [ ] 3.3 Run `assertNoOrphanedToolProtocol` validation

### Acceptance Criteria

- [ ] All compact.go tests pass
- [ ] All conversion_visibility_test.go tests pass
- [ ] No orphaned tool calls appear in test output
- [ ] Agent can run end-to-end without "tool call result does not follow tool call" errors

### Validation

- `go test ./pkg/compact/... -v`
- `go test ./pkg/agent/... -v -run "Orphan|Tool|Pairing"`
- `go test ./... -v -count=1`

### Notes

- Task: Test that agent runs without being marked orphaned
- Race condition was in `ensureToolCallPairing` - hiding toolResults but not removing stale toolCalls
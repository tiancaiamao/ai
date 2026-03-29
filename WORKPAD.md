## Workpad

```
host: local
branch: fixed-1774749181
```

### Plan
- [x] 1. Investigate orphaned check feature in codebase
- [x] 2. Understand grace period requirement
- [x] 3. Implement grace period feature for ensureToolCallPairing
- [x] 4. Write tests for the grace period feature
- [x] 5. Validate tests pass
- [ ] 6. Create PR and complete review

### Acceptance Criteria
- [x] Grace period implemented for orphaned check (protect N most recent tool results)
- [x] GracePeriod config field added with default value of 1
- [x] ensureToolCallPairingWithGrace method added
- [x] Tests pass for the grace period feature
- [ ] PR created and approved

### Validation
- [x] targeted tests: `go test ./pkg/compact/... -v`
- [x] all tests: `go test ./...`

### Implementation Summary

Added `GracePeriod` config field to protect N most recent tool results from being archived during compaction:

1. **Config field**: Added `GracePeriod int` to `Config` struct with default value of 1
2. **New method**: Added `ensureToolCallPairingWithGrace()` method that:
   - Protects the N most recent visible tool results from being archived
   - Grace period defaults to 1 if configured as 0
   - Older tool results still get archived as before
3. **Modified Compact()**: Now uses `ensureToolCallPairingWithGrace()` when `GracePeriod > 0`

### Notes
- Task title: "[FIXED] Orphaned Check"
- Task description: "Test with grace period"
- Implemented grace period protection for tool call pairing in compaction
- Default GracePeriod=1 protects the most recent tool result from being archived
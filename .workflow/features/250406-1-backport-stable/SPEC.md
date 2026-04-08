# Feature: Backport to Stable Base

## Summary
Revert to the pre-rewrite stable commit (e9b1a039) and selectively port back valuable changes from the context management rewrite (3755cdb..HEAD), excluding the broken context management implementation.

## Motivation
The context management rewrite at 3755cdb introduced an Event Sourcing + dual-mode architecture that has been unstable. 9 fix commits later, fundamental issues remain (e.g., 429 errors crash the entire conversation). The old codebase has better error handling and edge case coverage.

## User Stories
- As a developer, I want a stable codebase that doesn't crash on transient errors
- As a developer, I want the snapshot + checkpoint mechanism preserved
- As a developer, I want to re-implement context management as a simpler "mini compact" interface

## Requirements
- [x] Start from e9b1a039 (stable base)
- [ ] Port snapshot + checkpoint + journal mechanism (P0)
- [ ] Port duplicate message fix (P1)
- [ ] Port RPC response format fix (P1)
- [ ] Port steer/follow-up channel queue (P1)
- [ ] Port duplicate events fix for win mode (P2)
- [ ] Port ScriptedLLM test infrastructure (P2)
- [ ] DO NOT port dual-mode context management (ModeNormal/ModeContextMgmt)
- [ ] All tests pass after each port

## Out of Scope
- New "mini compact" implementation (separate feature)
- Porting loop_context_mgmt.go
- Porting legacy_compat.go

## Success Criteria
- [ ] All existing tests pass
- [ ] Binary builds successfully
- [ ] Checkpoint/journal system works
- [ ] Bug fixes (duplicate messages, RPC format) work
- [ ] No regressions from e9b1a039 baseline

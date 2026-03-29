# Workpad

## Plan

- [x] 1. Reproduction & Investigation
  - [x] 1.1 Read and understand current context_management.go implementation
  - [x] 1.2 Understand ContextMgmtState methods and fields
  - [x] 1.3 Review how skip is currently handled
  - [x] 1.4 Identify where to add skip limit logic

- [x] 2. Implementation Design
  - [x] 2.1 Calculate ratio = ProactiveDecisions - ReminderNeeded
  - [x] 2.2 Implement maxSkip = min(ratio, 30), floor at 0
  - [x] 2.3 Handle 3 cases: deny (ratio <= 0), reduce (skipTurns > maxSkip), success (skipTurns <= maxSkip)
  - [x] 2.4 Add appropriate error/warning messages

- [x] 3. Implementation
  - [x] 3.1 Add skip limit logic to context_management.go
  - [x] 3.2 Implement Case 1: Deny skip when ratio <= 0
  - [x] 3.3 Implement Case 2: Reduce skipTurns when over limit
  - [x] 3.4 Implement Case 3: Allow skip within limit
  - [x] 3.5 Update runtime_state reminders_remaining calculation

- [x] 4. Testing
  - [x] 4.1 Unit test 1: Test skip denied when ratio <= 0
  - [x] 4.2 Unit test 2: Test skip reduced when over limit
  - [x] 4.3 Unit test 3: Test skip capped at 30
  - [x] 4.4 Unit test 4: Test skip success when within limit
  - [x] 4.5 Unit test 5: Test reminders_remaining calculation

- [x] 5. Verification
  - [x] 5.1 Run all tests in pkg/tools/
  - [x] 5.2 Run tests in pkg/context/ for state management
  - [x] 5.3 Manual validation of error messages

- [x] 6. PR & Review
  - [x] 6.1 Commit changes
  - [x] 6.2 Push branch
  - [x] 6.3 Create PR with symphony label
  - [x] 6.4 Self-review using review skill
  - [x] 6.5 Address any review comments

## Self-Review Result

### Review #1 (Initial AI Review)
- **Status**: PASS - No P0/P1 findings
- **P2 Finding**: Test comment documentation clarity in TestContextManagementSkipLimitReducedWhenOverMax (comment says maxSkip=2 but setup suggests ratio=5)

### Review #2 (codex-rs subagent review)
- **Status**: PASS - No P0/P1 findings
- **Overall Correctness**: patch is correct
- **Confidence**: 0.95
- **Findings**: None
- **Explanation**: The skip limit logic is correctly implemented with proper handling of all three cases (deny, reduce, success). Edge cases for ratio <= 0, skipTurns > maxSkip, and maxSkip clamped at 30 are all handled correctly. The runtime_state reminders_remaining calculation properly accounts for SkipUntilTurn. All tests pass and cover the important scenarios.

### CI Status
- Build and Test: PASS (1m10s)
- Build and Test (claw): PASS (1m53s)

## Acceptance Criteria

- [x] LLM cannot skip when proactive ratio <= 0
- [x] Skip is reduced to max_skip when over limit
- [x] Max skip is capped at 30 even with high ratio
- [x] Clear error messages explain denial/reduction
- [x] reminders_remaining in runtime_state is accurate
- [x] All 5 unit tests pass
- [x] Integration test validates end-to-end behavior

## Validation

- [x] Unit tests: `go test ./pkg/tools -v -run TestSkip` - PASS
- [x] All tools tests: `go test ./pkg/tools -v` - PASS
- [x] Context tests: `go test ./pkg/context -v` - PASS
- [x] All tests: `go test ./...` - PASS

## Notes

- Implementation complete
- All tests pass
- Skip limit logic: maxSkip = max(0, min(30, ProactiveDecisions - ReminderNeeded))
- Runtime state calculation updated with SkipUntilTurn
- Error messages follow the spec format from issue #102

## Files Changed

- pkg/tools/context_management.go: Added skip limit logic with 3 cases
- pkg/context/context.go: Added SkipUntilTurn to ContextMgmtSnapshot
- pkg/tools/context_management_test.go: Added 5 new unit tests
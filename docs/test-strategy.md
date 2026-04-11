# Test Strategy

## Overview

This document outlines the testing strategy for the `ai` project, covering unit tests, integration tests, regression tests, and E2E (end-to-end) tests.

## Test Pyramid

```
        E2E Tests (74 benchmark tasks)
              ↓
      Integration Tests
              ↓
        Unit Tests
```

## Layer 1: Unit Tests (Fast, Focused)

### Purpose

Test individual functions and methods in isolation.

### Target Coverage

- **pkg/agent**: 71.2% → 80%
- **pkg/prompt**: 77.5% → 80%
- **pkg/skill**: 74.6% → 80%
- **pkg/tools**: 35.4% → 60%
- **pkg/rpc**: 24.8% → 50%
- **pkg/context**: 37.0% → 60%
- **pkg/llm**: 38.1% → 60%

### Examples

- `pkg/agent/agent_test.go`: Agent lifecycle
- `pkg/agent/loop_test.go`: Loop behavior
- `pkg/context/compactor_test.go`: Compaction logic
- `pkg/tools/bash_test.go`: Bash tool timeout
- `pkg/tools/executor_test.go`: Tool execution

### When to Write

- Implementing new business logic
- Fixing bugs (test first)
- Refactoring code
- Adding new guardrails

### Running Unit Tests

```bash
# Run all unit tests
go test ./...

# Run specific package
go test ./pkg/agent -v

# Run with coverage
go test -coverprofile=coverage.out ./...

# Generate HTML coverage report
go tool cover -html=coverage.out -o coverage.html

# Check coverage for specific package
go test -cover ./pkg/agent
```

## Layer 2: Integration Tests (Medium, Realistic)

### Purpose

Test component interactions.

### Target Coverage

- Agent + Tools integration
- RPC handlers + Agent
- Workflow + Orchestrate
- Task scheduling and dependencies
- Human-in-the-loop checkpoints

### Examples

- `pkg/agent/agent_integration_test.go`: Agent with tools
- `skills/workflow/orchestrate/integration_test.go`: Workflow execution
- RPC handler integration tests
- Session persistence tests

### When to Write

- Adding new integration points
- Changing component interfaces
- Testing error paths
- Validating cross-component behavior

### Running Integration Tests

```bash
# Run integration tests
go test -v ./pkg/agent -run Integration

# Run workflow integration tests
go test -v ./skills/workflow/orchestrate
```

## Layer 3: Regression Tests (Critical, Must Pass)

### Purpose

Prevent regression of historical bugs.

### Target

**100% pass rate** - All regression tests must pass.

### Test Categories

#### 1. Guardrail Configuration Tests (`pkg/agent/regression_test.go`)

Ensure all guardrails are properly configured and enforced:

- `TestRegression001_MaxConsecutiveToolCalls`: Prevent infinite tool call loops
- `TestRegression002_AutoCompactConfiguration`: Context overflow recovery
- `TestRegression003_ToolCallCutoffConfiguration`: Stale tool output truncation
- `TestRegression004_MaxToolCallsPerName`: Prevent tool abuse
- `TestRegression005_MaxTurnsConfiguration`: Prevent runaway conversations
- `TestRegression006_ContextWindowConfiguration`: Enforce context limits
- `TestRegression007_LLMRetryConfiguration`: Retry on rate limit errors
- `TestRegression008_ToolOutputLimits`: Large output truncation
- `TestRegression009_ExecutorPoolConfiguration`: Tool execution limits
- `TestRegression010_EnableCheckpointConfiguration`: Checkpoint control
- `TestRegression011_LLMTimeoutConfiguration`: LLM call timeouts
- `TestRegression012_RuntimeMetaConfiguration`: Runtime meta updates

#### 2. Behavior Regression Tests

Test that specific behaviors don't regress:

- Tool execution with timeout
- Context compaction triggers
- State persistence
- Error recovery
- Graceful degradation

### When to Write

- **Immediately after fixing a bug**: Add test to prevent regression
- Before refactoring guardrails
- When adding new safety features

### Running Regression Tests

```bash
# Run all regression tests
go test -v -run TestRegression ./...

# Run specific regression test
go test -v -run TestRegression001 ./pkg/agent

# In CI: All regression tests must pass
```

## Layer 4: E2E Tests (Slow, Realistic)

### Purpose

Test complete workflows and agent behaviors.

### Test Suite

74 benchmark tasks under `benchmark/tasks/`:

| Category | Tasks | Focus |
|----------|-------|-------|
| Agent Behavior | agent_001-010 | Exploration, debugging, memory |
| Context Management | agent_004, 010, 011 | Overflow, compaction |
| Tool Usage | agent_006 | Tool traps, misuse |
| Performance | agent_008 | Budget management |
| Code Generation | 001-013 | Various scenarios |

### Test Organization

#### Agent Behavior Tests (agent_001-010)

- `agent_001_forced_exploration`: Force agent to use grep efficiently
- `agent_002_debugging_session`: Debug complex issues
- `agent_003_...`: (various behavior tests)
- `agent_010_compact_tool_call_mismatch`: Context compaction edge cases

#### Context Management Tests

- `agent_004_*`: Context overflow handling
- `agent_010_*`: Compaction strategies
- `agent_011_*`: Context window enforcement

#### Tool Usage Tests

- `agent_006_*`: Tool traps and misuse patterns
- Tool execution with edge cases

#### Performance Tests

- `agent_008_*`: Budget and resource management
- Token estimation accuracy
- Cost enforcement

### Running E2E Tests

```bash
# Run all E2E tests
go test ./benchmark/...

# Run fast subset (< 10 min)
go test -fast ./benchmark/...

# Run specific test
go test -v ./benchmark/tasks/agent_001_forced_exploration

# Run with manifest (subset of tasks)
go test -manifest=fast-manifest.json ./benchmark/...
```

## Test Metrics

### Coverage Targets

| Package | Current | Target | Priority |
|---------|---------|--------|----------|
| pkg/agent | 71.2% | 80% | High |
| pkg/prompt | 77.5% | 80% | Medium |
| pkg/skill | 74.6% | 80% | Medium |
| pkg/tools | 35.4% | 60% | High |
| pkg/rpc | 24.8% | 50% | High |
| pkg/context | 37.0% | 60% | High |
| pkg/llm | 38.1% | 60% | Medium |

### Pass Rate Targets

- **Regression tests**: 100% (must pass)
- **Unit tests**: >95%
- **Integration tests**: >90%
- **E2E tests (fast subset)**: >90%

## Continuous Integration

### Test Pipeline

```yaml
# CI Pipeline (example)

stages:
  - unit_tests:
      - go test -cover ./...
      - go tool cover -func=coverage.out
      - Verify coverage >= targets

  - regression_tests:
      - go test -run TestRegression ./...
      - Verify 100% pass rate

  - integration_tests:
      - go test ./pkg/agent -run Integration
      - go test ./skills/workflow/orchestrate

  - e2e_tests:
      - go test -fast ./benchmark/...
      - go test -manifest=fast-manifest.json ./benchmark/...
```

### Pre-Commit Checks

```bash
#!/bin/bash
# scripts/pre-commit.sh

# Run unit tests for changed packages
go test ./$(changed_packages) -cover

# Run regression tests (must pass)
go test -run TestRegression ./...

# Quick smoke test
go test -run TestSmoke ./...
```

## Test Tools

### Go Test Flags

```bash
-v              # Verbose output
-cover          # Enable coverage
-coverprofile=  # Write coverage profile
-race           # Race detection
-parallel=      # Parallel execution
-count=1        # Run tests once (disable caching)
-timeout=       # Test timeout
-short          # Skip long tests
```

### Coverage Tools

```bash
# Generate coverage
go test -coverprofile=coverage.out ./...

# View coverage
go tool cover -html=coverage.out -o coverage.html
open coverage.html

# Check coverage percentage
go tool cover -func=coverage.out | grep total

# Check specific package
go tool cover -func=coverage.out | grep pkg/agent
```

### Benchmark Tests

```bash
# Run benchmarks
go test -bench=. -benchmem ./...

# Compare benchmarks
go test -bench=. -benchmem ./... > old.txt
# Make changes
go test -bench=. -benchmem ./... > new.txt
benchcmp old.txt new.txt
```

## Test Best Practices

### Unit Tests

✅ **Do:**
- Test single behavior per test
- Use descriptive test names
- Test happy paths and error paths
- Use table-driven tests for multiple cases
- Mock external dependencies
- Test edge cases

❌ **Don't:**
- Test multiple behaviors in one test
- Depend on external services
- Test implementation details (test behavior instead)
- Skip error cases
- Use timeouts as primary test mechanism

### Integration Tests

✅ **Do:**
- Test component interactions
- Use realistic data
- Test error paths
- Clean up after tests
- Use test fixtures

❌ **Don't:**
- Test implementation details of internal components
- Depend on external LLM (use mocks)
- Skip cleanup
- Test too many components at once

### Regression Tests

✅ **Do:**
- Add test immediately after fixing bug
- Test the specific bug scenario
- Make test deterministic
- Add comments explaining the bug
- Run before merging any PR

❌ **Don't:**
- Skip regression tests
- Remove tests when refactoring (update instead)
- Make tests flaky (avoid timeouts, race conditions)

### E2E Tests

✅ **Do:**
- Test complete workflows
- Use representative scenarios
- Document test purpose
- Organize by category
- Use timeouts appropriately

❌ **Don't:**
- Run too many tests in CI (use manifests)
- Make tests too slow (target <10min for fast subset)
- Skip tests silently

## Test Maintenance

### Adding Tests

1. Write test first (TDD)
2. Watch it fail
3. Implement minimal code to pass
4. Refactor
5. Run full test suite

### Updating Tests

1. When behavior changes intentionally:
   - Update test to match new behavior
   - Document why behavior changed

2. When fixing bugs:
   - Add regression test
   - Fix code to pass test
   - Run full test suite

### Removing Tests

- Only remove tests when:
  - Feature is being removed
  - Test is redundant (document reason)
  - Test is incorrect (replace with correct test)

## Test Documentation

### Test Comments

```go
// TestRegression001_MaxConsecutiveToolCalls tests that MaxConsecutiveToolCalls prevents infinite loops
//
// Bug: Agent gets stuck in tool call loop without progress
// Fix: Added MaxConsecutiveToolCalls limit in LoopConfig
func TestRegression001_MaxConsecutiveToolCalls(t *testing.T) {
    // ...
}
```

### Test Files

- Organize tests alongside code (e.g., `agent_test.go`)
- Use `*_test.go` suffix
- Keep tests readable and maintainable

## Troubleshooting

### Flaky Tests

Symptoms: Test passes sometimes, fails others.

Solutions:
- Remove randomness or use seeded random
- Remove dependencies on timing
- Use synchronization primitives
- Add proper cleanup

### Slow Tests

Symptoms: Tests take too long to run.

Solutions:
- Use mocks instead of real LLM
- Reduce test data size
- Parallelize independent tests
- Run only affected tests in CI

### Coverage Not Improving

Symptoms: Adding tests doesn't increase coverage.

Solutions:
- Check if tests are actually running
- Verify tests are testing new code paths
- Use `-coverprofile` to see what's not covered
- Focus on untested critical paths

## References

- Go testing: https://golang.org/pkg/testing/
- Table-driven tests: https://dave.cheney.net/2019/05/07/prefer-table-driven-tests
- Test coverage: https://blog.golang.org/cover
- Regression testing: https://en.wikipedia.org/wiki/Regression_testing
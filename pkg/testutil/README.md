# Testutil Package

The `testutil` package provides comprehensive testing utilities for the AI agent project, inspired by best practices from Crush, Goose, and Codex projects.

## Features

1. **VCR Recording/Replay** - Record LLM API calls and replay them without using API quota
2. **Mock LLM Client** - Test agent behavior without making real API calls
3. **Golden File Testing** - Snapshot testing for expected outputs
4. **Scenario Testing** - End-to-end behavior tests
5. **Test Environment** - Easy setup of temporary directories and configurations

## Quick Start

### Basic Test Environment

```go
import (
    "testing"
    "github.com/tiancaiamao/ai/pkg/testutil"
)

func TestMyFeature(t *testing.T) {
    env := testutil.Setup(t, testutil.SetupOptions{})
    if env == nil {
        return // Skipped if no config available
    }

    // Use env.TempDir, env.SessionDir, env.Model, env.APIKey
    // ...
}
```

### Minimal Test Environment (No API Required)

```go
func TestMyFeature_Mock(t *testing.T) {
    env := testutil.SetupWithMinimalConfig(t)

    // Use env without needing real API keys
    // ...
}
```

### VCR Recording/Replay

Record real API calls:
```bash
VCR_MODE=record go test -v ./pkg/agent -run TestMyScenario
```

Replay recorded calls (no API usage):
```bash
VCR_MODE=replay go test -v ./pkg/agent -run TestMyScenario
```

### Mock LLM Client

```go
import (
    "github.com/tiancaiamao/ai/pkg/testutil"
)

func TestWithMock(t *testing.T) {
    mockClient := testutil.NewMockLLMClient()
    mockClient.AddTextResponse("4")
    mockClient.AddTextResponse("The capital of France is Paris")

    // Use mockClient with your agent
    // ...
}
```

### Golden File Testing

Run tests:
```bash
go test -v ./pkg/agent -run TestGolden
```

Update golden files:
```bash
UPDATE_GOLDEN=1 go test -v ./pkg/agent -run TestGolden
```

## Directory Structure

```
pkg/
├── testutil/
│   ├── env.go              # Test environment setup
│   ├── recorder.go         # VCR recording/replay
│   ├── mock_llm.go         # Mock LLM client
│   └── testdata/
│       ├── vcr/             # Recorded HTTP interactions
│       │   ├── anthropic/
│       │   ├── openai/
│       │   └── zai/
│       └── golden/          # Golden file snapshots
├── agent/
│   ├── scenario_test.go    # Scenario tests
│   └── golden_test.go      # Golden file tests
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `VCR_MODE` | VCR mode: "record", "replay", or "disabled" | "disabled" |
| `VCR_PROVIDER` | Provider name for VCR recordings (e.g., "zai", "anthropic") | "default" |
| `UPDATE_GOLDEN` | Set to "1" to update golden files | "" |
| `AI_CONFIG_PATH` | Path to config.json (overrides default) | "" |

## Makefile Targets

```makefile
test:           ## Run all tests
	go test -v ./...

test-record:   ## Record VCR tests
	VCR_MODE=record go test -v ./pkg/agent -run TestScenarios_VCR

test-replay:   ## Replay VCR tests
	VCR_MODE=replay go test -v ./pkg/agent -run TestScenarios_VCR

test-golden:    ## Update golden files
	UPDATE_GOLDEN=1 go test -v ./pkg/agent -run TestGolden

test-scenario:  ## Run scenario tests
	go test -v ./pkg/agent -run TestScenarios

test-e2e:       ## Run E2E tests
	go test -v ./pkg/agent -run TestE2E

test-race:      ## Run tests with race detector
	go test -race -v ./...

test-short:     ## Run quick tests only
	go test -short -v ./...
```

## Best Practices

1. **Use VCR for LLM tests** - Record once, replay forever. Saves API quota and makes tests faster.
2. **Mock for unit tests** - Use mock LLM client for isolated unit tests.
3. **Golden files for snapshots** - Use golden files when output should be exact.
4. **Scenario tests for behavior** - Use scenario tests to verify end-to-end behavior.
5. **Minimal config for pure logic** - Use `SetupWithMinimalConfig` when testing doesn't need LLM.

## Examples

See:
- `pkg/agent/scenario_test.go` - Scenario test examples
- `pkg/agent/golden_test.go` - Golden file test examples
- `pkg/agent/e2e_suite_test.go` - E2E test examples

## References

This test framework is inspired by:
- **Crush** - VCR testing with `x/vcr`
- **Goose** - Test providers and scenario tests
- **Codex** - Comprehensive test infrastructure

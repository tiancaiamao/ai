.PHONY: build test clean vet coverage test-record test-replay test-golden test-scenario test-e2e test-race test-short test-vcr

# Default target
all: build

# Build the project
build:
	go build ./...

# Run tests with coverage
test:
	go test -short ./... -race -coverprofile=coverage.out -covermode=atomic -timeout 30s

# Run vet
vet:
	go vet ./...

# Download dependencies
deps:
	go mod download
	go mod verify

# Clean build artifacts
clean:
	rm -f coverage.out
	go clean

# Coverage report
coverage: test
	go tool cover -html=coverage.out -o coverage.html

# ============================================================================
# VCR Testing
# ============================================================================

# Record VCR tests (makes real API calls)
test-record:
	@echo "Recording VCR tests - this will make real API calls..."
	VCR_MODE=record go test -v ./pkg/agent -run TestScenarios_VCR -timeout 120s

# Replay VCR tests (uses recorded responses, no API calls)
test-replay:
	@echo "Replaying VCR tests - no API calls will be made..."
	VCR_MODE=replay go test -v ./pkg/agent -run TestScenarios_VCR -timeout 60s

# VCR test helper (alias for replay)
test-vcr: test-replay

# ============================================================================
# Golden File Testing
# ============================================================================

# Run golden file tests
test-golden:
	go test -v ./pkg/agent -run TestGolden -timeout 60s

# Update golden files (set UPDATE_GOLDEN=1)
test-golden-update:
	@echo "Updating golden files..."
	UPDATE_GOLDEN=1 go test -v ./pkg/agent -run TestGolden -timeout 60s

# ============================================================================
# Scenario Testing
# ============================================================================

# Run scenario tests
test-scenario:
	go test -v ./pkg/agent -run TestScenarios -timeout 180s

# Run scenario tests with mock (no API calls)
test-scenario-mock:
	go test -v ./pkg/agent -run TestScenarios_WithMock -timeout 30s

# ============================================================================
# E2E Testing
# ============================================================================

# Run all E2E tests
test-e2e:
	go test -v ./pkg/agent -run TestE2E -timeout 300s

# Run E2E tests by category
test-e2e-cat1:
	go test -v ./pkg/agent -run "TestE2E_1_" -timeout 60s

test-e2e-cat2:
	go test -v ./pkg/agent -run "TestE2E_2_" -timeout 120s

test-e2e-cat3:
	go test -v ./pkg/agent -run "TestE2E_3_" -timeout 60s

test-e2e-cat4:
	go test -v ./pkg/agent -run "TestE2E_4_" -timeout 60s

test-e2e-cat5:
	go test -v ./pkg/agent -run "TestE2E_5_" -timeout 120s

test-e2e-cat6:
	go test -v ./pkg/agent -run "TestE2E_6_" -timeout 120s

test-e2e-cat7:
	go test -v ./pkg/agent -run "TestE2E_7_" -timeout 60s

test-e2e-cat8:
	go test -v ./pkg/agent -run "TestE2E_8_" -timeout 60s

test-e2e-cat9:
	go test -v ./pkg/agent -run "TestE2E_9_" -timeout 60s

test-e2e-cat10:
	go test -v ./pkg/agent -run "TestE2E_10_" -timeout 30s

# ============================================================================
# Special Test Modes
# ============================================================================

# Run tests with race detector
test-race:
	go test -race -v ./... -timeout 180s

# Run quick tests only
test-short:
	go test -short -v ./...

# Run all tests (including slow ones)
test-all:
	go test -v ./... -timeout 600s

# Run tests for specific package
test-agent:
	go test -v ./pkg/agent -timeout 180s

test-llm:
	go test -v ./pkg/llm -timeout 60s

test-tools:
	go test -v ./pkg/tools -timeout 60s

test-context:
	go test -v ./pkg/context -timeout 60s

# ============================================================================
# CI/CD Helpers
# ============================================================================

# Run CI test suite (fast, no API calls)
test-ci:
	go test -short -race ./... -timeout 120s

# Run integration tests (requires API keys)
test-integration:
	go test -v ./pkg/agent -run "TestScenarios|TestE2E" -timeout 300s
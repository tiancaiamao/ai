.PHONY: build install test clean vet coverage fmt fmt-check coverage-check

# Default target
all: build

# Build the project (compile check only)
build:
	go build ./...

# Install all binaries to GOBIN (default: ~/go/bin)
install:
	go install ./cmd/ai

# Run tests with coverage
test:
	go test ./pkg/... -timeout 30s -coverprofile=coverage.out -covermode=atomic

# Run tests with race detector and coverage (may fail with "text file busy" on CI)
test-ci:
	go test -short ./pkg/... -race -coverprofile=coverage.out -covermode=atomic -timeout 30s

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

# Check that pkg/ coverage meets the 80% threshold.
# Used by CI to gate merges on coverage regressions.
# Run `make test` first to generate coverage.out.
COVERAGE_THRESHOLD := 80
coverage-check:
	@test -f coverage.out || { echo "ERROR: coverage.out not found. Run 'make test' first."; exit 1; }
	@head -1 coverage.out > /tmp/pkg-cov.out
				@grep '/pkg/' coverage.out | grep -v '/pkg/rpc/' >> /tmp/pkg-cov.out
	@pct=$$(go tool cover -func=/tmp/pkg-cov.out | tail -1 | awk '{print $$NF}' | tr -d '%'); \
	rm -f /tmp/pkg-cov.out; \
	echo "pkg/ coverage: $$pct% (threshold: $(COVERAGE_THRESHOLD)%)"; \
	if awk -v p="$$pct" 'BEGIN { exit (p+0 < $(COVERAGE_THRESHOLD)) ? 1 : 0 }'; then \
		echo "✅ Coverage check passed"; \
	else \
		echo "❌ Coverage $$pct% is below $(COVERAGE_THRESHOLD)% threshold"; \
		exit 1; \
	fi

# Run E2E tests against a real OpenAI-compatible model endpoint.
# NOT part of CI: requires a reachable model. Defaults to the first ollama/*
# model from ~/.ai/models.json (prefers "laguna"); override with
# E2E_BASE_URL / E2E_MODEL / E2E_API_KEY. Tests skip when the endpoint is
# unreachable or unconfigured.
e2e:
	go test -tags e2e ./pkg/e2e/ -v -count=1 -timeout 30m

# Format code
fmt:
	gofmt -w .

# Check that code is formatted (fails if any file needs formatting)
fmt-check:
	@output=$$(gofmt -l .); \
	if [ -n "$$output" ]; then \
		echo "ERROR: the following files need formatting (run 'make fmt'):"; \
		echo "$$output"; \
		exit 1; \
	fi

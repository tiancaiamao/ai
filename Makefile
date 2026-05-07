.PHONY: build install test clean vet coverage

# Default target
all: build

# Build the project (compile check only)
build:
	go build ./...

# Install all binaries to GOBIN (default: ~/go/bin)
install:
	go install ./cmd/ai
	cd claw && go install ./cmd/aiclaw

# Run tests with coverage
test:
	go test -short ./... -timeout 30s

# Run tests with race detector and coverage (may fail with "text file busy" on CI)
test-ci:
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
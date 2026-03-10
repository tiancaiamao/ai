.PHONY: build test clean vet coverage

# Default target
all: build

# Build the aiclaw binary
build:
	go build -o bin/aiclaw ./cmd/aiclaw

# Run tests with coverage
test:
	go test -short ./... -race -coverprofile=coverage.out -covermode=atomic

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
	rm -rf bin/
	go clean

# Coverage report
coverage: test
	go tool cover -html=coverage.out -o coverage.html
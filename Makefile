.PHONY: build test clean vet coverage

# Default target
all: build

# Build the project
build:
	go build ./...

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
	go clean

# Coverage report
coverage: test
	go tool cover -html=coverage.out -o coverage.html
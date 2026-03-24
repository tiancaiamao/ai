.PHONY: build test clean vet coverage bench bench-build bench-run bench-resume bench-list bench-compare bench-baseline bench-clean bench-precheck reset reset-hard ab-list ab-test ab-compare ab-benchmark ab-report help

# Default target
all: build

# Build the project
build:
	go build ./...

# Benchmark commands (alias to benchmark/Makefile)
bench bench-build bench-run bench-resume bench-list bench-compare bench-baseline bench-clean bench-precheck reset reset-hard ab-list ab-test ab-compare ab-benchmark ab-report help:
	$(MAKE) -C benchmark $@

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
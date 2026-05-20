#!/bin/bash
set -e

TASK_DIR="$(cd "$(dirname "$0")" && pwd)"

# Ensure setup/ exists with a fresh copy of init
if [ ! -d "$TASK_DIR/setup" ] || [ -z "$(ls -A "$TASK_DIR/setup" 2>/dev/null)" ]; then
  rm -rf "$TASK_DIR/setup"
  cp -R "$TASK_DIR/init" "$TASK_DIR/setup"
fi

cd "$TASK_DIR/setup"

# Test 1: Check implementation file exists
if [ ! -f "lru_cache.go" ]; then
  echo "FAIL: lru_cache.go not found — create your implementation"
  exit 1
fi

# Test 2: Code compiles
echo "--- Building ---"
go build ./...

# Test 3: Vet
echo "--- go vet ---"
go vet ./...

# Test 4: Run all tests
echo "--- Running tests ---"
go test -v -count=1 -timeout 60s ./...

echo ""
echo "All tests passed!"
exit 0
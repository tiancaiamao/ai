#!/bin/bash
# Verification script for HTTP parser unit tests task

cd "$(dirname "$0")/setup"

# Test 1: Check if tests compile
if ! go test -c ./httpfile -o /dev/null 2>/dev/null; then
    # Try building the package first
    if ! go build ./httpfile 2>/dev/null; then
        echo "FAIL: HTTP parser code does not compile"
        exit 1
    fi
fi
echo "PASS: Code compiles"

# Test 2: Check if test files exist
test_files=$(find . -name "*_test.go" 2>/dev/null | head -1)
if [ -z "$test_files" ]; then
    echo "FAIL: No test files found (*_test.go)"
    exit 1
fi
echo "PASS: Test files exist"

# Test 3: Run the tests
output=$(go test ./httpfile -v 2>&1)
test_result=$?

if [ $test_result -eq 0 ]; then
    # Count passed tests
    passed=$(echo "$output" | grep -c "PASS" || true)
    echo "PASS: Tests pass ($passed test assertions)"
else
    echo "FAIL: Tests failed"
    echo "$output" | tail -20
    exit 1
fi

# Test 4: Check for testdata directory (Go convention)
if [ -d "httpfile/testdata" ] || [ -d "testdata" ]; then
    echo "PASS: Uses testdata directory convention"
else
    echo "WARN: No testdata directory found (optional but recommended)"
fi

echo ""
echo "All tests passed!"
exit 0

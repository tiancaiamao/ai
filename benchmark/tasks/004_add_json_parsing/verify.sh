#!/bin/bash
# Verification script for JSON error handling task

cd "$(dirname "$0")/setup"

# Test 1: Check if the code compiles
if ! go build -o /dev/null 2>/dev/null; then
    echo "FAIL: Code does not compile"
    exit 1
fi
echo "PASS: Code compiles"

# Test 2: Check if parseJSON returns error type
if grep -q "func parseJSON.*error" main.go; then
    echo "PASS: parseJSON returns error"
else
    echo "FAIL: parseJSON should return (map, error)"
    exit 1
fi

# Test 3: Check if toJSON returns error type
if grep -q "func toJSON.*error" main.go || grep -q "func toJSON.*\[\]byte" main.go; then
    echo "PASS: toJSON returns error or byte slice"
else
    echo "FAIL: toJSON should return ([]byte, error) or (string, error)"
    exit 1
fi

echo ""
echo "All tests passed!"
exit 0

#!/bin/bash
# Verification script for task 002_add_error_handling

cd "$(dirname "$0")/setup"

# Test 1: Check if the code compiles
if ! go build -o /dev/null main.go 2>/dev/null; then
    echo "FAIL: Code does not compile"
    exit 1
fi
echo "PASS: Code compiles"

# Test 2: Check if Divide has error return
if grep -q "Divide.*float64.*error" main.go; then
    echo "PASS: Divide returns error"
else
    echo "FAIL: Divide should return (float64, error)"
    exit 1
fi

# Test 3: Check if GetUserAge has error return
if grep -q "GetUserAge.*int.*error" main.go; then
    echo "PASS: GetUserAge returns error"
else
    echo "FAIL: GetUserAge should return (int, error)"
    exit 1
fi

# Test 4: Run the program (should not panic)
if go run main.go > /dev/null 2>&1; then
    echo "PASS: Program runs without panic"
else
    echo "FAIL: Program crashes"
    exit 1
fi

echo "All tests passed!"
exit 0

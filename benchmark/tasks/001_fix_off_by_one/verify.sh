#!/bin/bash
# Verification script for task 001_fix_off_by_one

cd "$(dirname "$0")/setup"

# Test 1: Check if SumRange(5) returns 15
output=$(go run main.go)
if [ "$output" = "15" ]; then
    echo "PASS: SumRange(5) = 15"
else
    echo "FAIL: SumRange(5) expected 15, got $output"
    exit 1
fi

# Test 2: Verify the code change
if grep -q "i <= n" main.go; then
    echo "PASS: Loop condition fixed to i <= n"
else
    echo "FAIL: Loop condition not fixed"
    exit 1
fi

echo "All tests passed!"
exit 0

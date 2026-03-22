#!/bin/bash
# Verification script for task 020_hashline_ambiguous_edit

cd "$(dirname "$0")/setup"

# Test 1: Verify only ONE occurrence of "<= len(items)" exists (ProcessorB only)
count=$(grep -c "<= len(items)" duplicate_code.go)
if [ "$count" -eq 1 ]; then
    echo "PASS: Only one occurrence of '<= len(items)' found (ProcessorB only)"
else
    echo "FAIL: Found $count occurrences of '<= len(items)', expected exactly 1"
    exit 1
fi

# Test 2: Verify TWO occurrences of "< len(items)" exist (ProcessorA and ProcessorC)
count=$(grep -c "< len(items)" duplicate_code.go)
if [ "$count" -eq 2 ]; then
    echo "PASS: Two occurrences of '< len(items)' found (ProcessorA and ProcessorC)"
else
    echo "FAIL: Found $count occurrences of '< len(items)', expected exactly 2"
    exit 1
fi

# Test 3: Run the program and verify output
output=$(go run duplicate_code.go)
if [ "$output" = "All processors passed!" ]; then
    echo "PASS: Program output correct"
else
    echo "FAIL: Expected 'All processors passed!', got: $output"
    exit 1
fi

echo "All tests passed!"
exit 0
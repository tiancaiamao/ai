#!/bin/bash
# Verification script for concurrent bug fix task

cd "$(dirname "$0")/setup"

# Test 1: Check if the code compiles
if ! go build -o counter 2>/dev/null; then
    echo "FAIL: Code does not compile"
    exit 1
fi
echo "PASS: Code compiles"

# Test 2: Run multiple times to check for race condition
success_count=0
fail_count=0
runs=5

for i in $(seq 1 $runs); do
    output=$(./counter 2>&1)
    if echo "$output" | grep -q "SUCCESS"; then
        success_count=$((success_count + 1))
    else
        fail_count=$((fail_count + 1))
    fi
done

echo "PASS: $success_count/$runs runs succeeded"

if [ $success_count -eq $runs ]; then
    echo "PASS: No race condition detected"
else
    echo "FAIL: Race condition still present ($fail_count failures)"
    exit 1
fi

# Test 3: Check if mutex or atomic is used
if grep -q "Mutex\|atomic" main.go; then
    echo "PASS: Uses proper synchronization (Mutex or atomic)"
else
    echo "WARN: No obvious synchronization primitive found"
fi

echo ""
echo "All tests passed!"
exit 0

#!/bin/bash
# Verification script for BASIC interpreter task

cd "$(dirname "$0")/setup"

# Test 1: Check if the code compiles
if ! go build -o basic 2>/dev/null; then
    echo "FAIL: Code does not compile"
    exit 1
fi
echo "PASS: Code compiles"

# Test 2: example1.bas - Simple print
output=$(echo "" | timeout 2 ./basic examples/example1.bas 2>/dev/null)
if [ "$output" = "hello" ]; then
    echo "PASS: example1.bas outputs 'hello'"
else
    echo "FAIL: example1.bas expected 'hello', got '$output'"
    exit 1
fi

# Test 3: example3.bas - FOR loop (should print hello 10 times)
output=$(echo "" | timeout 2 ./basic examples/example3.bas 2>/dev/null)
expected=$(printf 'hello\nhello\nhello\nhello\nhello\nhello\nhello\nhello\nhello\nhello')
if [ "$output" = "$expected" ]; then
    echo "PASS: example3.bas FOR loop works correctly"
else
    echo "FAIL: example3.bas FOR loop incorrect"
    echo "Expected 10 'hello' lines, got:"
    echo "$output" | head -15
    exit 1
fi

# Test 4: example5.bas - String input
output=$(echo "World" | timeout 2 ./basic examples/example5.bas 2>/dev/null)
if echo "$output" | grep -q "hello World"; then
    echo "PASS: example5.bas string input works"
else
    echo "FAIL: example5.bas string input failed"
    echo "Output: $output"
    exit 1
fi

# Test 5: example6.bas - Numeric input
output=$(echo "5" | timeout 2 ./basic examples/example6.bas 2>/dev/null)
if echo "$output" | grep -q "double is 10"; then
    echo "PASS: example6.bas arithmetic works"
else
    echo "FAIL: example6.bas arithmetic failed"
    echo "Output: $output"
    exit 1
fi

# Test 6: Check that infinite loop programs don't hang forever
# example2.bas is an infinite loop, should be terminated by timeout
if timeout 1 ./basic examples/example2.bas >/dev/null 2>&1; then
    echo "WARN: example2.bas should be an infinite loop but terminated quickly"
else
    echo "PASS: Infinite loop handled by timeout"
fi

echo ""
echo "All tests passed!"
exit 0

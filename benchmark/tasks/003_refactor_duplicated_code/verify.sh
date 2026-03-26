#!/bin/bash
# Verification script for task 003_refactor_duplicated_code

cd "$(dirname "$0")/setup"

# Test 1: Check if the code compiles
if ! go build -o /dev/null main.go 2>/dev/null; then
    echo "FAIL: Code does not compile"
    exit 1
fi
echo "PASS: Code compiles"

# Test 2: Check that output is unchanged
expected="User: Alice (age: 30, email: alice@example.com)
Product: Widget (price: 29.99, category: Electronics)
Order: ORD-001 (amount: 99.99, status: pending)"

actual=$(go run main.go)

if [ "$actual" = "$expected" ]; then
    echo "PASS: Output matches expected"
else
    echo "FAIL: Output mismatch"
    echo "Expected:"
    echo "$expected"
    echo "Got:"
    echo "$actual"
    exit 1
fi

# Test 3: Check for reduced code duplication (file should be smaller)
lines=$(wc -l < main.go)
if [ "$lines" -lt 60 ]; then
    echo "PASS: Code appears refactored ($lines lines)"
else
    echo "FAIL: Code is too long ($lines lines), needs refactoring to under 60 lines"
    exit 1
fi

# Test 4: Check that a helper function exists
if grep -q "func.*[Vv]alid" main.go || grep -q "func.*[Cc]heck" main.go; then
    echo "PASS: Helper function detected"
else
    echo "FAIL: No helper function found for validation"
    exit 1
fi

echo "All tests passed!"
exit 0

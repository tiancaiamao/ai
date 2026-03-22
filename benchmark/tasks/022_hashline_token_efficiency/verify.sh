#!/bin/bash
# Verification script for task 022_hashline_token_efficiency

cd "$(dirname "$0")/setup"

# Test 1: Check if models/user.go has Age as int
if grep -q "Age\s*int" models/user.go; then
    echo "PASS: models/user.go - Age is now int type"
else
    echo "FAIL: models/user.go - Age is still string type"
    exit 1
fi

# Test 2: Check if models/user.go does NOT have Age as string
if grep -q "Age\s*string" models/user.go; then
    echo "FAIL: models/user.go - Age is still string type (should be int)"
    exit 1
fi

# Test 3: Check if services/user_service.go uses Age field correctly
if grep -q "user.Age" services/user_service.go; then
    echo "PASS: services/user_service.go - uses Age field"
else
    echo "FAIL: services/user_service.go - does not use Age field"
    exit 1
fi

# Test 4: Check if main.go references user.Age (not user.UserAge)
if grep -q 'Age:\s*"25"' main.go; then
    echo "PASS: main.go - uses Age field"
else
    echo "FAIL: main.go - does not use Age field correctly"
    exit 1
fi

# Test 5: Run the program and verify output
output=$(go run main.go models/user.go services/user_service.go 2>&1)
if echo "$output" | grep -q "All validations passed!"; then
    echo "PASS: Program output correct"
else
    echo "FAIL: Expected 'All validations passed!', got: $output"
    exit 1
fi

echo "All tests passed!"
exit 0
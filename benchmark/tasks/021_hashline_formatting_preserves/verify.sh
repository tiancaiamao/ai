#!/bin/bash
# Verification script for task 021_hashline_formatting_preserves

cd "$(dirname "$0")/setup"

# Test 1: Check if CalculateTotal uses addition for discount
if grep -q "return order.Price \* float64(order.Quantity) + order.Discount" poorly_formatted.go; then
    echo "PASS: CalculateTotal bug fixed (addition)"
else
    echo "FAIL: CalculateTotal still has subtraction bug"
    exit 1
fi

# Test 2: Check if GetTaxRate returns 0.10
if grep -q "return 0.10" poorly_formatted.go; then
    echo "PASS: GetTaxRate bug fixed (0.10)"
else
    echo "FAIL: GetTaxRate still returns wrong value"
    exit 1
fi

# Test 3: Run the program and verify output
output=$(go run poorly_formatted.go)
if [ "$output" = "All calculations passed!" ]; then
    echo "PASS: Program output correct"
else
    echo "FAIL: Expected 'All calculations passed!', got: $output"
    exit 1
fi

# Test 4: Verify bugs are NOT present
if grep -q "return order.Price \* float64(order.Quantity) - order.Discount" poorly_formatted.go; then
    echo "FAIL: Old subtraction bug still present"
    exit 1
fi

if grep -q "return 0.08" poorly_formatted.go; then
    echo "FAIL: Old tax rate 0.08 still present"
    exit 1
fi

echo "All tests passed!"
exit 0
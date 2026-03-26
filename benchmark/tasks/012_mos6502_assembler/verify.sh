#!/bin/bash
# Verification script for MOS6502 assembler task

cd "$(dirname "$0")/setup"

# Test 1: Check if the code compiles
if ! go build -o assembler 2>/dev/null; then
    echo "FAIL: Code does not compile"
    exit 1
fi
echo "PASS: Code compiles"

# Test 2: Check if simple_load.asm assembles
if [ -f "examples/simple_load.asm" ]; then
    output=$(./assembler examples/simple_load.asm 2>&1)
    if [ $? -eq 0 ]; then
        echo "PASS: simple_load.asm assembles"
    else
        echo "FAIL: simple_load.asm failed to assemble"
        echo "$output"
        exit 1
    fi
else
    echo "SKIP: simple_load.asm not found"
fi

# Test 3: Check output is valid JSON
output=$(./assembler examples/simple_load.asm 2>/dev/null)
if echo "$output" | python3 -c "import json,sys; json.load(sys.stdin)" 2>/dev/null; then
    echo "PASS: Output is valid JSON"
else
    echo "FAIL: Output is not valid JSON"
    echo "$output"
    exit 1
fi

# Test 4: Check JSON has required fields
if echo "$output" | python3 -c "import json,sys; d=json.load(sys.stdin); assert 'symbols' in d; assert 'instructions' in d" 2>/dev/null; then
    echo "PASS: JSON has required fields (symbols, instructions)"
else
    echo "FAIL: JSON missing required fields"
    exit 1
fi

# Test 5: Count successfully assembled files
pass_count=0
total_count=0
for f in examples/*.asm; do
    if [ -f "$f" ]; then
        total_count=$((total_count + 1))
        if ./assembler "$f" >/dev/null 2>&1; then
            pass_count=$((pass_count + 1))
        fi
    fi
done

echo "PASS: Assembled $pass_count/$total_count example files"

if [ $pass_count -lt 5 ]; then
    echo "FAIL: Need at least 5 files to assemble correctly"
    exit 1
fi

echo ""
echo "All tests passed!"
exit 0

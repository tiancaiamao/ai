#!/bin/bash

set -e

echo "Testing /context command (deadlock fix)..."

# Build the binary
echo "Building ai..."
cd "$(dirname "$0")/.."
go build -o ai ./cmd/ai

# Test directory
TEST_DIR="/tmp/ai-context-test-$$"
mkdir -p "$TEST_DIR"
cd "$TEST_DIR"

echo ""
echo "Test 1: /context command should not deadlock"
echo "=============================================="

# Create input with /context command
cat > input.jsonl << 'EOF'
{"type":"prompt","message":"/context"}
{"type":"prompt","message":"/quit"}
EOF

# Run ai with timeout and capture output
echo "Running ai with /context command..."
timeout 10 "$OLDPWD/ai" --mode rpc < input.jsonl > output.jsonl 2>&1 || {
    EXIT_CODE=$?
    if [ $EXIT_CODE -eq 124 ]; then
        echo "❌ TIMEOUT: ai process hung for 10 seconds (likely deadlock)"
        echo ""
        echo "Output before timeout:"
        cat output.jsonl
        rm -rf "$TEST_DIR"
        exit 1
    fi
}

echo "✓ ai completed within timeout (no deadlock)"

# Check for expected output
if grep -q "Context Usage" output.jsonl; then
    echo "✓ Found 'Context Usage' in output"
else
    echo "❌ Expected 'Context Usage' not found in output"
    echo ""
    echo "Full output:"
    cat output.jsonl
    rm -rf "$TEST_DIR"
    exit 1
fi

# Check for progress bar characters
if grep -q "⛁\|⛶" output.jsonl; then
    echo "✓ Found progress bar characters in output"
else
    echo "⚠ Warning: Progress bar characters not found in output"
fi

echo ""
echo "Test 2: /show usage deprecation notice"
echo "======================================="

cat > input2.jsonl << 'EOF'
{"type":"prompt","message":"/show usage"}
{"type":"prompt","message":"/quit"}
EOF

timeout 10 "$OLDPWD/ai" --mode rpc < input2.jsonl > output2.jsonl 2>&1 || {
    EXIT_CODE=$?
    if [ $EXIT_CODE -eq 124 ]; then
        echo "❌ TIMEOUT: ai process hung (deadlock)"
        rm -rf "$TEST_DIR"
        exit 1
    fi
}

if grep -q "integrated into /context" output2.jsonl; then
    echo "✓ /show usage shows deprecation notice"
else
    echo "❌ /show usage deprecation not found"
    echo ""
    echo "Full output:"
    cat output2.jsonl
    rm -rf "$TEST_DIR"
    exit 1
fi

echo ""
echo "==================================="
echo "✅ All tests passed!"
echo "==================================="

# Show sample output
echo ""
echo "Sample /context output:"
echo "----------------------"
grep "Context Usage\|⛁\|⛶\|Session Stats\|Model:" output.jsonl | head -10

# Cleanup
rm -rf "$TEST_DIR"

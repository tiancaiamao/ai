#!/bin/bash
# Live test script for ai

# Source common test utilities
source scripts/test-common.sh

# Check prerequisites
check_ai_binary
check_api_key true

print_header "ai Live Test"

print_section "Test 1" "Simple prompt (no abort, wait for completion)"
(printf '{"type":"prompt","message":"Say hi"}\n'; sleep 60) | timeout 65 "$AI_BIN" 2>&1 | head -100 &
PID=$!

# Wait for output
sleep 10

echo ""
echo "=== Test completed (killed after 10 seconds) ==="

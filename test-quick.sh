#!/bin/bash
# Quick test script for ai

# Source common test utilities
source scripts/test-common.sh

# Check prerequisites
check_ai_binary
check_api_key true

print_header "ai Quick Test"

# Create a temporary input file
cat > /tmp/ai-test-input.txt << 'EOF'
{"type":"prompt","message":"Say hi to me"}
{"type":"abort"}
EOF

print_section "Test 1" "Simple prompt"
echo "Running: cat /tmp/ai-test-input.txt | $AI_BIN"
cat /tmp/ai-test-input.txt | timeout 60 "$AI_BIN" 2>&1 | grep -E '(server_start|message_update|response|agent_end|error)' | head -30

# Clean up
cleanup_temp_files

echo ""
echo "=== Test completed ==="

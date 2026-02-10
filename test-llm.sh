#!/bin/bash
# Test script for LLM integration
# Make sure ZAI_API_KEY is set before running

# Source common test utilities
source scripts/test-common.sh

# Check prerequisites
check_ai_binary
check_api_key true

print_header "ai LLM Integration Test"

# Test 1: Simple prompt
print_section "Test 1" "Sending prompt 'Hello, please say hi back'"
echo '{"type":"prompt","message":"Hello! Please just say hi back to me."}' | timeout 30 "$AI_BIN" | grep -E '(server_start|message_update|response)' | head -20

wait_for_ai 2

# Test 2: Get state
print_section "Test 2" "Getting agent state"
echo '{"type":"get_state"}' | timeout 5 "$AI_BIN" | grep '"type":"response"'

echo ""
echo "=== Tests completed ==="

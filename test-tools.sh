#!/bin/bash
# Test script for tool system
# Make sure ZAI_API_KEY is set before running

# Source common test utilities
source scripts/test-common.sh

# Check prerequisites
check_ai_binary
check_api_key true

print_header "ai Tool System Test"

# Create a test file
echo "Hello, this is a test file for ai." > test-file.txt

print_section "Test 1" "Read tool"
echo '{"type":"prompt","message":"Read the file test-file.txt and tell me what it contains"}' | timeout 30 "$AI_BIN" | grep -E '(server_start|tool_execution_start|tool_execution_end|message_update)' | head -20

wait_for_ai 2

print_section "Test 2" "Bash tool"
echo '{"type":"prompt","message":"List the files in the current directory using ls"}' | timeout 30 "$AI_BIN" | grep -E '(tool_execution_start|tool_execution_end|message_update)' | head -20

wait_for_ai 2

print_section "Test 3" "Write tool"
echo '{"type":"prompt","message":"Create a file called hello.txt with the content '\''Hello from ai!'\''"}' | timeout 30 "$AI_BIN" | grep -E '(tool_execution_start|tool_execution_end|message_update)' | head -20

wait_for_ai 2

print_section "Test 4" "Grep tool"
echo '{"type":"prompt","message":"Search for '\''main'\'' in all Go files using grep"}' | timeout 30 "$AI_BIN" | grep -E '(tool_execution_start|tool_execution_end|message_update)' | head -20

# Clean up test files
cleanup_temp_files

echo ""
echo "=== Tests completed ==="
echo ""
echo "To manually test:"
echo "1. Start ai: ZAI_API_KEY=sk-xxx ./bin/ai"
echo "2. In another terminal:"
echo "   echo '{\"type\":\"prompt\",\"message\":\"Read README.md\"}' | ./bin/ai"

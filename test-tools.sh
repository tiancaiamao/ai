#!/bin/bash

# Test script for tool system
# Make sure ZAI_API_KEY is set before running

cd "$(dirname "$0")"

AUTH_FILE="${AI_AUTH_FILE:-$HOME/.ai/auth.json}"
if [ -z "$ZAI_API_KEY" ] && [ ! -f "$AUTH_FILE" ]; then
	echo "Error: ZAI_API_KEY is not set and $AUTH_FILE not found"
	echo "Usage: ZAI_API_KEY=sk-xxx ./test-tools.sh"
	echo "Or create auth file with provider credentials"
	exit 1
fi

echo "=== ai Tool System Test ==="
echo ""

# Create a test file
echo "Hello, this is a test file for ai." > test-file.txt

echo "Test 1: Read tool"
echo '{"type":"prompt","message":"Read the file test-file.txt and tell me what it contains"}' | timeout 30 ./bin/ai | grep -E '(server_start|tool_execution_start|tool_execution_end|message_update)' | head -20
echo ""

sleep 2

echo "Test 2: Bash tool"
echo '{"type":"prompt","message":"List the files in the current directory using ls"}' | timeout 30 ./bin/ai | grep -E '(tool_execution_start|tool_execution_end|message_update)' | head -20
echo ""

sleep 2

echo "Test 3: Write tool"
echo '{"type":"prompt","message":"Create a file called hello.txt with the content '\''Hello from ai!'\''"}' | timeout 30 ./bin/ai | grep -E '(tool_execution_start|tool_execution_end|message_update)' | head -20
echo ""

sleep 2

echo "Test 4: Grep tool"
echo '{"type":"prompt","message":"Search for '\''main'\'' in all Go files using grep"}' | timeout 30 ./bin/ai | grep -E '(tool_execution_start|tool_execution_end|message_update)' | head -20
echo ""

# Clean up test files
rm -f test-file.txt hello.txt

echo "=== Tests completed ==="
echo ""
echo "To manually test:"
echo "1. Start ai: ZAI_API_KEY=sk-xxx ./bin/ai"
echo "2. In another terminal:"
echo "   echo '{\"type\":\"prompt\",\"message\":\"Read README.md\"}' | ./bin/ai"

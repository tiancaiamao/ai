#!/bin/bash

# Test script for LLM integration
# Make sure ZAI_API_KEY is set before running

cd "$(dirname "$0")"

AUTH_FILE="${AI_AUTH_FILE:-$HOME/.ai/auth.json}"
if [ -z "$ZAI_API_KEY" ] && [ ! -f "$AUTH_FILE" ]; then
	echo "Error: ZAI_API_KEY is not set and $AUTH_FILE not found"
	echo "Usage: ZAI_API_KEY=sk-xxx ./test-llm.sh"
	echo "Or create auth file with provider credentials"
	exit 1
fi

echo "=== ai LLM Integration Test ==="
echo ""

# Test 1: Simple prompt
echo "Test 1: Sending prompt 'Hello, please say hi back'"
echo '{"type":"prompt","message":"Hello! Please just say hi back to me."}' | timeout 30 ./bin/ai | grep -E '(server_start|message_update|response)' | head -20
echo ""

# Wait a bit between tests
sleep 2

# Test 2: Get state
echo "Test 2: Getting agent state"
echo '{"type":"get_state"}' | timeout 5 ./bin/ai | grep '"type":"response"'
echo ""

echo "=== Tests completed ==="

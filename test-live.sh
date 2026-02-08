#!/bin/bash

cd "$(dirname "$0")"

AUTH_FILE="${AI_AUTH_FILE:-$HOME/.ai/auth.json}"
if [ -z "$ZAI_API_KEY" ] && [ ! -f "$AUTH_FILE" ]; then
	echo "Error: ZAI_API_KEY is not set and $AUTH_FILE not found"
	echo "Set ZAI_API_KEY or create an auth file with provider credentials"
	exit 1
fi

echo "=== Testing ai Tool System ==="
echo ""

echo "Test 1: Simple prompt (no abort, wait for completion)"
(printf '{"type":"prompt","message":"Say hi"}\n'; sleep 60) | timeout 65 ./bin/ai 2>&1 | head -100 &
PID=$!

# Wait for output
sleep 10

echo ""
echo "=== Test completed (killed after 10 seconds) ==="

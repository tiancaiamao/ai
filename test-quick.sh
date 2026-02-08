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

# Create a temporary input file
cat > /tmp/ai-test-input.txt << 'EOF'
{"type":"prompt","message":"Say hi to me"}
{"type":"abort"}
EOF

echo "Test 1: Simple prompt"
echo "Running: cat /tmp/ai-test-input.txt | ./bin/ai"
cat /tmp/ai-test-input.txt | timeout 60 ./bin/ai 2>&1 | grep -E '(server_start|message_update|response|agent_end|error)' | head -30
echo ""

# Clean up
rm -f /tmp/ai-test-input.txt

echo "=== Test completed ==="

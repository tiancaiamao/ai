#!/bin/bash
# Test script for /session command

echo "=== Testing AI RPC get_state command ==="
echo ""

# Start the AI server in the background
./bin/ai --mode rpc > /tmp/ai_output.log 2>&1 &
AI_PID=$!

# Give it time to start
sleep 1

# Send the get_state command
echo '{"command":"get_state","id":"test-1"}' | nc localhost 4242 || echo "Failed to connect"

# Wait a bit then kill the background process
sleep 1
kill $AI_PID 2>/dev/null

echo ""
echo "=== AI Output ==="
cat /tmp/ai_output.log | grep -E "get_state|SessionFile|sessionFile" | head -20
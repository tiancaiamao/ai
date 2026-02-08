#!/bin/bash

cd "$(dirname "$0")"

# Test get_state
echo "Test 1: get_state"
echo '{"type":"get_state","id":"test-1"}' | ./bin/ai | grep '"type":"response"'
echo ""

# Test prompt
echo "Test 2: prompt"
echo '{"type":"prompt","message":"Hello"}' | ./bin/ai | head -5
echo ""

# Test abort
echo "Test 3: abort"
echo '{"type":"abort"}' | ./bin/ai | head -3

#!/bin/bash
# RPC test script for ai

# Source common test utilities
source scripts/test-common.sh

# Check prerequisites
check_ai_binary

# Note: test-rpc doesn't require API key for basic RPC tests

print_header "ai RPC Test"

print_section "Test 1" "get_state"
echo '{"type":"get_state","id":"test-1"}' | "$AI_BIN" | grep '"type":"response"'

echo ""

print_section "Test 2" "prompt"
echo '{"type":"prompt","message":"Hello"}' | "$AI_BIN" | head -5

echo ""

print_section "Test 3" "abort"
echo '{"type":"abort"}' | "$AI_BIN" | head -3

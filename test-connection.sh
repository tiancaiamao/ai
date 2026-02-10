#!/bin/bash
# ai LLM Connection Test Script
# This script helps diagnose API connectivity issues

# Source common test utilities
source scripts/test-common.sh

# Check prerequisites
check_ai_binary

print_header "ai LLM Connection Test"

print_section "1" "Checking environment variables"
check_api_key false
check_env_vars true

echo ""
print_section "2" "Testing DNS resolution"

# Determine base URL
if [ -z "$ZAI_BASE_URL" ]; then
	# Default URL from main.go
	BASE_URL="https://api.z.ai/api/coding/paas/v4"
else
	BASE_URL="$ZAI_BASE_URL"
fi

# Extract hostname from URL
HOST=$(echo "$BASE_URL" | sed -e 's|^[^/]*//||' -e 's|/.*$||')
echo "   Testing host: $HOST"

if command -v host > /dev/null 2>&1; then
	if host "$HOST" > /dev/null 2>&1; then
		print_result pass "DNS resolution successful"
	else
		print_result fail "DNS resolution failed for '$HOST'"
		echo "   This host may not exist or require VPN"
	fi
else
	print_result info "host command not available, skipping DNS test"
fi

echo ""
print_section "3" "Testing HTTP connection"
if command -v curl > /dev/null 2>&1; then
	HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -m 5 "$BASE_URL/chat/completions" 2>&1 || echo "000")
	case "$HTTP_CODE" in
		"000")
			print_result fail "Connection failed (timeout or DNS error)"
			;;
		"401")
			print_result pass "Server reachable (401 = needs valid API key)"
			;;
		"404")
			print_result warn "Server reachable but endpoint not found"
			;;
		"400")
			print_result pass "Server reachable (400 = bad request format)"
			;;
		*)
			print_result info "HTTP response code: $HTTP_CODE"
			;;
	esac
else
	print_result warn "curl not installed, skipping HTTP test"
fi

echo ""
print_section "4" "Common API endpoints to try"
echo "   - OpenAI:        export ZAI_BASE_URL=https://api.openai.com/v1"
echo "   - Azure OpenAI:  export ZAI_BASE_URL=https://your-resource.openai.azure.com"
echo "   - Local server:  export ZAI_BASE_URL=http://localhost:11434/v1"
echo "   - Other:         Check your API provider's documentation"

echo ""
print_section "5" "Quick test with OpenAI (if you have a key)"
echo "   export ZAI_BASE_URL=https://api.openai.com/v1"
echo "   export ZAI_API_KEY=sk-your-openai-key"
echo "   export ZAI_MODEL=gpt-4"
echo "   $AI_BIN"

echo ""
echo "=========================================="
echo "Test complete!"
echo "=========================================="

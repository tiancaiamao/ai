#!/bin/bash

# ai LLM Connection Test Script
# This script helps diagnose API connectivity issues

cd "$(dirname "$0")"
AUTH_FILE="${AI_AUTH_FILE:-$HOME/.ai/auth.json}"

echo "=========================================="
echo "  ai - LLM Connection Test"
echo "=========================================="
echo ""

# Check environment variables
echo "1. Checking environment variables..."
if [ -z "$ZAI_API_KEY" ]; then
    if [ -f "$AUTH_FILE" ]; then
        echo "   ℹ️  ZAI_API_KEY is not set (using auth file: $AUTH_FILE)"
    else
        echo "   ❌ ZAI_API_KEY is not set"
        echo "   Please set: export ZAI_API_KEY=your-key-here"
    fi
else
    echo "   ✅ ZAI_API_KEY is set (${#ZAI_API_KEY} chars)"
fi

if [ -z "$ZAI_BASE_URL" ]; then
    echo "   ⚠️  ZAI_BASE_URL is not set (using default)"
else
    echo "   ✅ ZAI_BASE_URL=$ZAI_BASE_URL"
fi

if [ -z "$ZAI_MODEL" ]; then
    echo "   ⚠️  ZAI_MODEL is not set (using default: gpt-4)"
else
    echo "   ✅ ZAI_MODEL=$ZAI_MODEL"
fi

echo ""
echo "2. Testing DNS resolution..."
if [ -z "$ZAI_BASE_URL" ]; then
    # Default URL from main.go
    BASE_URL="https://api.zai.ai/v1"
else
    BASE_URL="$ZAI_BASE_URL"
fi

# Extract hostname from URL
HOST=$(echo "$BASE_URL" | sed -e 's|^[^/]*//||' -e 's|/.*$||')
echo "   Testing host: $HOST"

if host "$HOST" > /dev/null 2>&1; then
    echo "   ✅ DNS resolution successful"
else
    echo "   ❌ DNS resolution failed for '$HOST'"
    echo "   This host may not exist or require VPN"
fi

echo ""
echo "3. Testing HTTP connection..."
if command -v curl > /dev/null 2>&1; then
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -m 5 "$BASE_URL/chat/completions" 2>&1 || echo "000")
    if [ "$HTTP_CODE" = "000" ]; then
        echo "   ❌ Connection failed (timeout or DNS error)"
    elif [ "$HTTP_CODE" = "401" ]; then
        echo "   ✅ Server reachable (401 = needs valid API key)"
    elif [ "$HTTP_CODE" = "404" ]; then
        echo "   ⚠️  Server reachable but endpoint not found"
    elif [ "$HTTP_CODE" = "400" ]; then
        echo "   ✅ Server reachable (400 = bad request format)"
    else
        echo "   ℹ️  HTTP response code: $HTTP_CODE"
    fi
else
    echo "   ⚠️  curl not installed, skipping HTTP test"
fi

echo ""
echo "4. Common API endpoints to try:"
echo "   - OpenAI:        export ZAI_BASE_URL=https://api.openai.com/v1"
echo "   - Azure OpenAI:  export ZAI_BASE_URL=https://your-resource.openai.azure.com"
echo "   - Local server:  export ZAI_BASE_URL=http://localhost:11434/v1"
echo "   - Other:         Check your API provider's documentation"

echo ""
echo "5. Quick test with OpenAI (if you have a key):"
echo "   export ZAI_BASE_URL=https://api.openai.com/v1"
echo "   export ZAI_API_KEY=sk-your-openai-key"
echo "   export ZAI_MODEL=gpt-4"
echo "   ./bin/ai"

echo ""
echo "=========================================="
echo "Test complete!"
echo "=========================================="

#!/bin/bash

# Simple test script for mcp-zai
# Downloads a test image and analyzes it with Z.AI

set -e

echo "🧪 Testing MCP Z.AI Image Analysis"
echo "=================================="
echo ""

# Download test image
TEST_IMAGE="/tmp/test-mcp-pig.png"
echo "📥 Downloading test image..."
curl -s -o "$TEST_IMAGE" "https://httpbin.org/image/png"
echo "✓ Image downloaded: $TEST_IMAGE"
echo ""

# Get API key from auth.json
API_KEY=$(jq -r '.zai.key' ~/.ai/auth.json)
echo "🔑 API Key: ${API_KEY:0:20}..."
echo ""

# Test analysis
echo "🔍 Analyzing image..."
echo ""

# Build JSON-RPC requests
INIT_REQUEST='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"mcp-zai-test","version":"1.0.0"}}}'

TOOL_REQUEST=$(cat <<EOF
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"analyze_image","arguments":{"image_source":"$TEST_IMAGE","prompt":"Describe this image in detail"}}}
EOF
)

# Call Z.AI MCP server
export ANTHROPIC_AUTH_TOKEN="$API_KEY"

echo "Sending requests to Z.AI MCP server..."
echo ""

COMBINED="$INIT_REQUEST"$'\n'"$TOOL_REQUEST"
RESPONSE=$(echo "$COMBINED" | npx -y @z_ai/mcp-server 2>&1 | grep '^{.*}$')

# Extract the result (last line)
RESULT=$(echo "$RESPONSE" | tail -1 | jq -r '.result.content[0].text // empty')

if [ -z "$RESULT" ]; then
    echo "❌ Failed to extract result"
    echo "Response: $RESPONSE"
    exit 1
fi

echo "✓ Analysis successful!"
echo ""
echo "📝 Analysis Result:"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "$RESULT"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

echo "✅ Test completed successfully!"

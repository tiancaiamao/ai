#!/bin/bash

# mcp-zai.sh - Image analysis using Z.AI GLM-4V model via MCP stdio protocol
# Usage: mcp-zai.sh <mode> <image> <prompt>
#   mode: Analysis mode (ocr, ui-to-code, diagram, chart, error, analyze)
#   image: Image URL or local file path
#   prompt: Description of what to analyze

set -e

# Check arguments
if [ -z "$1" ] || [ -z "$2" ] || [ -z "$3" ]; then
    cat <<USAGE >&2
Error: Missing required arguments

Usage: mcp-zai.sh <mode> <image> <prompt>

Modes:
  ocr          Extract text from images
  ui-to-code   Convert UI designs to code
  diagram      Understand technical diagrams
  chart        Analyze data visualizations
  error        Diagnose error screenshots
  analyze      General image analysis

Arguments:
  mode         Analysis mode (see list above)
  image        Image URL or local file path
  prompt       Description of what to analyze or extract

Examples:
  mcp-zai.sh ocr "screenshot.png" "Extract all text"
  mcp-zai.sh ui-to-code "design.png" "Describe the layout structure"
  mcp-zai.sh diagram "arch.png" "Explain this architecture"
  mcp-zai.sh analyze "https://example.com/image.png" "Describe what you see"

Environment Variables:
  ZAI_API_KEY    Z.AI API key (auto-read from ~/.ai/auth.json)

To get an API key, visit: https://open.bigmodel.cn
USAGE
    exit 1
fi

MODE="$1"
IMAGE="$2"
PROMPT="$3"

# Validate mode
VALID_MODES=("ocr" "ui-to-code" "diagram" "chart" "error" "analyze")

if [[ ! " ${VALID_MODES[@]} " =~ " ${MODE} " ]]; then
    echo "Error: Invalid mode '$MODE'" >&2
    echo "Valid modes: ${VALID_MODES[*]}" >&2
    exit 1
fi

# Load API key from multiple sources (priority: env var > .env file > ~/.ai/auth.json)
API_KEY="${ZAI_API_KEY:-}"

if [ -z "$API_KEY" ]; then
    # Try to load from .env file in script directory
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    ENV_FILE="$SCRIPT_DIR/.env"

    if [ -f "$ENV_FILE" ]; then
        API_KEY=$(grep "^ZAI_API_KEY=" "$ENV_FILE" | cut -d'=' -f2 | tr -d '"\n' || echo "")
    fi
fi

if [ -z "$API_KEY" ]; then
    # Try to load from ~/.ai/auth.json
    AUTH_FILE="$HOME/.ai/auth.json"

    if [ -f "$AUTH_FILE" ]; then
        API_KEY=$(jq -r '.zai.key // empty' "$AUTH_FILE" 2>/dev/null || echo "")
    fi
fi

if [ -z "$API_KEY" ]; then
    cat <<ERROR >&2
Error: ZAI_API_KEY not set

To use this skill, you need a Z.AI API key:

Option 1 - Set as environment variable:
  export ZAI_API_KEY="your_api_key_here"

Option 2 - Create .env file in this directory:
  echo "ZAI_API_KEY=your_api_key_here" > .env

Option 3 - Add to ~/.ai/auth.json:
  {
    "zai": { "type": "api_key", "key": "your_api_key_here" }
  }

Get your API key from: https://open.bigmodel.cn
ERROR
    exit 1
fi

# Determine if image is URL or local file
IMAGE_SOURCE="$IMAGE"

if [[ ! "$IMAGE" =~ ^https?:// ]]; then
    # It's a local file, check if it exists
    if [ ! -f "$IMAGE" ]; then
        echo "Error: Local file not found: $IMAGE" >&2
        exit 1
    fi

    # Convert to absolute path
    IMAGE_SOURCE="$(cd "$(dirname "$IMAGE")" && pwd)/$(basename "$IMAGE")"
fi

# Map mode to Z.AI MCP tool name
TOOL_NAME="analyze_image"

case "$MODE" in
    ocr)
        TOOL_NAME="extract_text_from_screenshot"
        ENHANCED_PROMPT="Extract all text content from this image. Preserve formatting and structure where possible. $PROMPT"
        ;;
    ui-to-code)
        TOOL_NAME="ui_to_artifact"
        ENHANCED_PROMPT="Describe in detail the layout structure, color style, main components, and interactive elements of the website in this image to facilitate subsequent code generation by the model. $PROMPT"
        ;;
    diagram)
        TOOL_NAME="understand_technical_diagram"
        ENHANCED_PROMPT="Analyze this technical diagram. Explain the structure, components, relationships, and flow shown. $PROMPT"
        ;;
    chart)
        TOOL_NAME="analyze_data_visualization"
        ENHANCED_PROMPT="Analyze this data visualization. Extract trends, patterns, key metrics, and insights. $PROMPT"
        ;;
    error)
        TOOL_NAME="diagnose_error_screenshot"
        ENHANCED_PROMPT="Analyze this error screenshot. Identify the problem, explain what's wrong, and suggest solutions. $PROMPT"
        ;;
    analyze)
        TOOL_NAME="analyze_image"
        ENHANCED_PROMPT="$PROMPT"
        ;;
esac

# Create JSON-RPC request for MCP initialization
INIT_REQUEST=$(cat <<EOF
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2024-11-05",
    "capabilities": {},
    "clientInfo": {
      "name": "mcp-zai-client",
      "version": "1.0.0"
    }
  }
}
EOF
)

# Create JSON-RPC request for tool call
TOOL_REQUEST=$(cat <<EOF
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "$TOOL_NAME",
    "arguments": {
      "image_source": "$IMAGE_SOURCE",
      "prompt": "$ENHANCED_PROMPT"
    }
  }
}
EOF
)

# Start Z.AI MCP server and communicate via stdio
# Set API key as environment variable for the MCP server
# Note: Z.AI MCP server uses ANTHROPIC_AUTH_TOKEN, not ZAI_API_KEY
export ANTHROPIC_AUTH_TOKEN="$API_KEY"

# Combine initialization and tool call
COMBINED_REQUEST="$INIT_REQUEST"$'\n'"$TOOL_REQUEST"

# Call the MCP server via npx
# Keep both stdout and stderr, then filter JSON responses
ALL_OUTPUT=$(echo "$COMBINED_REQUEST" | npx -y @z_ai/mcp-server 2>&1 || echo "")

# Extract only JSON lines (responses)
RESPONSE=$(echo "$ALL_OUTPUT" | grep '^{.*}$' || echo "")

# Check if response is empty
if [ -z "$RESPONSE" ]; then
    echo "Error: Failed to get response from Z.AI MCP server" >&2
    echo "Possible causes:" >&2
    echo "  - Node.js or npx not installed" >&2
    echo "  - Network connectivity issues" >&2
    echo "  - Invalid API key" >&2
    echo "  - Image file not accessible" >&2
    echo "  - Image file too large (max 8MB)" >&2
    echo "" >&2
    echo "Debug info:" >&2
    echo "  Image: $IMAGE_SOURCE" >&2
    echo "  Tool: $TOOL_NAME" >&2
    echo "  API Key: ${API_KEY:0:20}..." >&2
    exit 1
fi

# Parse JSON-RPC response
# MCP servers send multiple JSON lines, we need the last one (tool call result)
LAST_LINE=$(echo "$RESPONSE" | tail -1)

ERROR=$(echo "$LAST_LINE" | jq -r '.error.message // .error // ""' 2>/dev/null || echo "")

if [ -n "$ERROR" ]; then
    echo "Error: $ERROR" >&2

    # Check for rate limit error
    if [[ "$ERROR" == *"rate limit"* ]] || [[ "$ERROR" == *"429"* ]]; then
        echo "Hint: Rate limit exceeded. Please wait before making more requests." >&2
    fi

    # Show raw response for debugging
    echo "" >&2
    echo "Raw response (last line):" >&2
    echo "$LAST_LINE" >&2
    exit 1
fi

# Extract analysis result from response
# MCP response format: {"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"..."}]}}
RESULT=$(echo "$LAST_LINE" | jq -r '.result.content[0].text // .result // empty' 2>/dev/null)

if [ -z "$RESULT" ]; then
    echo "Error: Unable to extract analysis result from response" >&2
    echo "" >&2
    echo "Debug info:" >&2
    echo "Response (last line):" >&2
    echo "$LAST_LINE" >&2
    echo "" >&2
    echo "Full response:" >&2
    echo "$RESPONSE" >&2
    exit 1
fi

# Output the analysis
echo "$RESULT"

exit 0

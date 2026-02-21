#!/bin/bash

# mcp-context7.sh - Query Context7 for latest API documentation
# Usage: mcp-context7.sh <package> [version] [symbol]
#   package: Package name (e.g., "react", "go stdlib", "python:requests")
#   version: Package version (optional, attempts to auto-detect from project)
#   symbol: Specific API symbol to query (optional, returns general docs if omitted)

set -e

# Check if package is provided
if [ -z "$1" ]; then
    cat <<USAGE >&2
Error: Package name is required

Usage: mcp-context7.sh <package> [version] [symbol]

Arguments:
  package  Package name (examples: "react", "go stdlib", "python:requests")
  version  Package version (optional, auto-detected from project if omitted)
  symbol   Specific API symbol (optional, returns general docs if omitted)

Examples:
  mcp-context7.sh react 18.2.0 useState
  mcp-context7.sh go stdlib "http.NewRequest"
  mcp-context7.sh python requests "requests.get"

Environment Variables:
  CONTEXT7_API_KEY    Context7 API key (get from https://context7.ai)
  CONTEXT7_ENDPOINT   API endpoint (default: https://context7.ai/api/v1/query)

To get an API key, visit: https://context7.ai
USAGE
    exit 1
fi

PACKAGE="$1"
VERSION="${2:-auto}"
SYMBOL="${3:-}"

# Load API key from multiple sources (priority: env var > .env file > ~/.ai/auth.json)
API_KEY="${CONTEXT7_API_KEY:-}"

if [ -z "$API_KEY" ]; then
    # Try to load from .env file in script directory
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    ENV_FILE="$SCRIPT_DIR/.env"

    if [ -f "$ENV_FILE" ]; then
        API_KEY=$(grep "^CONTEXT7_API_KEY=" "$ENV_FILE" | cut -d'=' -f2 | tr -d '"\n' || echo "")
    fi
fi

if [ -z "$API_KEY" ]; then
    # Try to load from ~/.ai/auth.json
    AUTH_FILE="$HOME/.ai/auth.json"

    if [ -f "$AUTH_FILE" ]; then
        API_KEY=$(jq -r '.context7.key // empty' "$AUTH_FILE" 2>/dev/null || echo "")
    fi
fi

if [ -z "$API_KEY" ]; then
    cat <<ERROR >&2
Error: CONTEXT7_API_KEY not set

To use this skill, you need a Context7 API key:

Option 1 - Set as environment variable:
  export CONTEXT7_API_KEY="your_api_key_here"

Option 2 - Create .env file in this directory:
  echo "CONTEXT7_API_KEY=your_api_key_here" > .env

Option 3 - Add to ~/.ai/auth.json:
  {
    "context7": { "type": "api_key", "key": "your_api_key_here" }
  }

Get your API key from: https://context7.ai
ERROR
    exit 1
fi

# API endpoint (can be overridden)
ENDPOINT="${CONTEXT7_ENDPOINT:-https://context7.ai/api/v1/query}"

# Auto-detect version if not provided and version is "auto"
if [ "$VERSION" = "auto" ]; then
    # Try to detect from common project files
    if [ -f "package.json" ]; then
        VERSION=$(jq -r ".dependencies.\"$PACKAGE\" // .devDependencies.\"$PACKAGE\" // \"unknown\"" package.json 2>/dev/null || echo "unknown")
    elif [ -f "go.mod" ]; then
        # For Go modules, try to get version from go.mod
        VERSION=$(grep "$PACKAGE" go.mod | awk '{print $2}' || echo "latest")
    elif [ -f "requirements.txt" ]; then
        # For Python packages
        VERSION=$(grep "^${PACKAGE}==" requirements.txt | cut -d'=' -f3 || echo "latest")
    else
        VERSION="latest"
    fi
fi

# Build JSON request payload
REQUEST_FILE=$(mktemp)
trap "rm -f $REQUEST_FILE" EXIT

if [ -n "$SYMBOL" ]; then
    # Query for specific symbol
    cat > "$REQUEST_FILE" <<EOF
{
  "package": "$PACKAGE",
  "version": "$VERSION",
  "symbol": "$SYMBOL"
}
EOF
else
    # General package documentation
    cat > "$REQUEST_FILE" <<EOF
{
  "package": "$PACKAGE",
  "version": "$VERSION"
}
EOF
fi

# Make API request
RESPONSE=$(curl -s \
    -X POST \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $API_KEY" \
    -d @"$REQUEST_FILE" \
    "$ENDPOINT" 2>/dev/null || echo "")

# Check if response is empty
if [ -z "$RESPONSE" ]; then
    echo "Error: Failed to get response from Context7 API" >&2
    echo "Possible causes:" >&2
    echo "  - Network connectivity issues" >&2
    echo "  - Invalid API key" >&2
    echo "  - API endpoint is down" >&2
    exit 1
fi

# Parse JSON response
# Expected format: {"documentation": "...", "version": "...", "examples": [...]}
ERROR_MSG=$(echo "$RESPONSE" | jq -r '.error // .message // ""' 2>/dev/null || echo "")

if [ -n "$ERROR_MSG" ]; then
    echo "Error: $ERROR_MSG" >&2
    exit 1
fi

# Extract documentation
DOCUMENTATION=$(echo "$RESPONSE" | jq -r '.documentation // "No documentation found"' 2>/dev/null || echo "Failed to parse response")
VERSION_INFO=$(echo "$RESPONSE" | jq -r '.version // "unknown"' 2>/dev/null)
EXAMPLES=$(echo "$RESPONSE" | jq -r '.examples[]? // empty' 2>/dev/null)

# Output formatted documentation
echo "# Context7: $PACKAGE $VERSION_INFO"
echo ""
echo "$DOCUMENTATION"
echo ""

# Add examples if available
if [ -n "$EXAMPLES" ]; then
    echo "## Examples"
    echo ""
    echo "$EXAMPLES"
    echo ""
fi

exit 0

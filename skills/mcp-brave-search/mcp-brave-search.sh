#!/bin/bash

# mcp-brave-search.sh - Search the web using Brave Search API
# Usage: mcp-brave-search.sh <query> [options]
#   query: Search query (required)
#   Options:
#     --time-recent <filter>   Time filter (oneDay, oneWeek, oneMonth, oneYear, noLimit)
#     --domain <domain>        Restrict to specific domain
#     --exclude-domain <domain> Exclude specific domain
#     --count <number>         Number of results (default: 10, max: 20)
#     --offset <number>        Pagination offset (default: 0)

set -e

# Default values
COUNT=10
OFFSET=0
TIME_FILTER=""
DOMAIN_FILTER=""
EXCLUDE_DOMAIN=""

# Parse arguments
QUERY=""

while [[ $# -gt 0 ]]; do
    case $1 in
        --time-recent)
            TIME_FILTER="$2"
            shift 2
            ;;
        --domain)
            DOMAIN_FILTER="$2"
            shift 2
            ;;
        --exclude-domain)
            EXCLUDE_DOMAIN="$2"
            shift 2
            ;;
        --count)
            COUNT="$2"
            shift 2
            ;;
        --offset)
            OFFSET="$2"
            shift 2
            ;;
        *)
            if [ -z "$QUERY" ]; then
                QUERY="$1"
            else
                QUERY="$QUERY $1"
            fi
            shift
            ;;
    esac
done

# Check if query is provided
if [ -z "$QUERY" ]; then
    cat <<USAGE >&2
Error: Search query is required

Usage: mcp-brave-search.sh <query> [options]

Options:
  --time-recent <filter>   Time filter: oneDay, oneWeek, oneMonth, oneYear, noLimit
  --domain <domain>        Restrict search to specific domain
  --exclude-domain <domain> Exclude specific domain from results
  --count <number>         Number of results (default: 10, max: 20)
  --offset <number>        Pagination offset (default: 0)

Examples:
  mcp-brave-search.sh "Model Context Protocol"
  mcp-brave-search.sh "AI agents 2025" --time-recent oneWeek
  mcp-brave-search.sh "MCP servers" --domain github.com
  mcp-brave-search.sh "climate change" --exclude-domain twitter.com

Environment Variables:
  BRAVE_API_KEY    Brave Search API key (get from https://api.search.brave.com/app/keys)

To get a free API key, visit: https://api.search.brave.com/app/keys
USAGE
    exit 1
fi

# Load API key from multiple sources (priority: env var > .env file > ~/.ai/auth.json)
API_KEY="${BRAVE_API_KEY:-}"

if [ -z "$API_KEY" ]; then
    # Try to load from .env file in script directory
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    ENV_FILE="$SCRIPT_DIR/.env"

    if [ -f "$ENV_FILE" ]; then
        API_KEY=$(grep "^BRAVE_API_KEY=" "$ENV_FILE" | cut -d'=' -f2 | tr -d '"\n' || echo "")
    fi
fi

if [ -z "$API_KEY" ]; then
    # Try to load from ~/.ai/auth.json
    AUTH_FILE="$HOME/.ai/auth.json"

    if [ -f "$AUTH_FILE" ]; then
        API_KEY=$(jq -r '.brave.key // .braveSearch.key // empty' "$AUTH_FILE" 2>/dev/null || echo "")
    fi
fi

if [ -z "$API_KEY" ]; then
    cat <<ERROR >&2
Error: BRAVE_API_KEY not set

To use this skill, you need a Brave Search API key:

Option 1 - Set as environment variable:
  export BRAVE_API_KEY="your_api_key_here"

Option 2 - Create .env file in this directory:
  echo "BRAVE_API_KEY=your_api_key_here" > .env

Option 3 - Add to ~/.ai/auth.json:
  {
    "braveSearch": { "type": "api_key", "key": "your_api_key_here" }
  }

Get your free API key from: https://api.search.brave.com/app/keys
ERROR
    exit 1
fi

# Build API endpoint URL
BASE_URL="https://api.search.brave.com/res/v1/web/search"

# URL-encode the query
ENCODED_QUERY=$(echo "$QUERY" | jq -sRr @uri)

# Build query parameters
PARAMS="q=$ENCODED_QUERY&count=$COUNT&offset=$OFFSET"

if [ -n "$TIME_FILTER" ]; then
    PARAMS="$PARAMS&freshness=$TIME_FILTER"
fi

if [ -n "$DOMAIN_FILTER" ]; then
    PARAMS="$PARAMS&site_filter=$DOMAIN_FILTER"
fi

if [ -n "$EXCLUDE_DOMAIN" ]; then
    PARAMS="$PARAMS&exclude_sites=$EXCLUDE_DOMAIN"
fi

FULL_URL="${BASE_URL}?${PARAMS}"

# Make API request
RESPONSE=$(curl -s \
    -X GET \
    -H "Accept: application/json" \
    -H "Accept-Encoding: gzip" \
    -H "X-Subscription-Token: $API_KEY" \
    "$FULL_URL" 2>/dev/null || echo "")

# Check if response is empty
if [ -z "$RESPONSE" ]; then
    echo "Error: Failed to get response from Brave Search API" >&2
    echo "Possible causes:" >&2
    echo "  - Network connectivity issues" >&2
    echo "  - Invalid API key" >&2
    echo "  - Rate limit exceeded" >&2
    exit 1
fi

# Check for errors in response
ERROR=$(echo "$RESPONSE" | jq -r '.error // .message // ""' 2>/dev/null || echo "")

if [ -n "$ERROR" ]; then
    echo "Error: $ERROR" >&2
    exit 1
fi

# Parse and format search results
TOTAL_RESULTS=$(echo "$RESPONSE" | jq -r '.results.web.total_results // "0"' 2>/dev/null)

echo "# Search Results for: \"$QUERY\""
echo ""
echo "**Total results:** $TOTAL_RESULTS"
echo ""

# Extract web results
RESULTS=$(echo "$RESPONSE" | jq -r '.results.web.results[]? // empty' 2>/dev/null)

if [ -z "$RESULTS" ]; then
    echo "No results found."
    exit 0
fi

# Format each result
INDEX=1
echo "$RESPONSE" | jq -r '.results.web.results[]? | @json' 2>/dev/null | while IFS= read -r RESULT; do
    if [ -n "$RESULT" ]; then
        TITLE=$(echo "$RESULT" | jq -r '.title // "No title"' 2>/dev/null)
        URL=$(echo "$RESULT" | jq -r '.url // ""' 2>/dev/null)
        SNIPPET=$(echo "$RESULT" | jq -r '.description // .snippet // "No description"' 2>/dev/null)
        PUBLISHED_DATE=$(echo "$RESULT" | jq -r '.published // ""' 2>/dev/null)

        echo "## Result $INDEX: $TITLE"
        echo ""
        echo "**URL:** $URL"

        if [ -n "$PUBLISHED_DATE" ] && [ "$PUBLISHED_DATE" != "null" ]; then
            echo "**Published:** $PUBLISHED_DATE"
        fi

        echo ""
        echo "$SNIPPET"
        echo ""
        echo "---"
        echo ""

        INDEX=$((INDEX + 1))
    fi
done

exit 0

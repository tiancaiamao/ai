#!/bin/bash

# mcp-fetch.sh - Simple web content fetcher
# Usage: mcp-fetch.sh <url> [format]
#   url: URL to fetch (required)
#   format: Output format (optional): text, markdown, json, auto (default: auto)

set -e

# Check if URL is provided
if [ -z "$1" ]; then
    cat <<USAGE >&2
Error: URL is required

Usage: mcp-fetch.sh <url> [format]

Arguments:
  url     URL to fetch (http:// or https://)
  format  Output format: text, markdown, json, auto (default: auto)

Examples:
  mcp-fetch.sh "https://example.com"
  mcp-fetch.sh "https://api.github.com/repos/modelcontextprotocol/servers" json
  mcp-fetch.sh "https://example.com/article.html" markdown

Features:
  - Automatically converts HTML to Markdown
  - Supports JSON API responses
  - Handles redirects
  - Returns formatted content
USAGE
    exit 1
fi

URL="$1"
FORMAT="${2:-auto}"

# Validate URL format
if [[ ! "$URL" =~ ^https?:// ]]; then
    echo "Error: URL must start with http:// or https://" >&2
    exit 1
fi

# Check dependencies
if ! command -v curl >/dev/null 2>&1; then
    echo "Error: curl is required but not installed" >&2
    exit 1
fi

# Fetch the content
echo "Fetching: $URL"
echo ""

# Fetch with curl
HTTP_CODE=$(curl -L -s -o /tmp/mcp-fetch-content -w "%{http_code}" "$URL" 2>&1)

if [ "$HTTP_CODE" != "200" ]; then
    echo "Error: HTTP $HTTP_CODE - Failed to fetch URL" >&2
    exit 1
fi

# Determine content type
CONTENT_TYPE=$(curl -s -I "$URL" | grep -i "content-type:" | cut -d':' -f2 | tr -d ' \r\n')

echo "Content-Type: $CONTENT_TYPE"
echo ""

# Process based on format or content type
case "$FORMAT" in
    "json")
        # Output as-is (JSON)
        cat /tmp/mcp-fetch-content
        ;;

    "text")
        # Extract text only (rough approximation)
        lynx -dump -stdin /tmp/mcp-fetch-content 2>/dev/null || \
            html2text -nobs /tmp/mcp-fetch-content 2>/dev/null || \
            cat /tmp/mcp-fetch-content
        ;;

    "markdown"|"auto")
        # Try to convert to Markdown
        if command -v pandoc >/dev/null 2>&1; then
            # Use pandoc for best results
            pandoc /tmp/mcp-fetch-content -f html -t markdown -o /tmp/mcp-fetch-md 2>/dev/null
            cat /tmp/mcp-fetch-md
        elif command -v lynx >/dev/null 2>&1; then
            # Use lynx as fallback
            lynx -dump -stdin /tmp/mcp-fetch-content 2>/dev/null || cat /tmp/mcp-fetch-content
        else
            # Raw output
            cat /tmp/mcp-fetch-content
        fi
        ;;

    *)
        # Auto-detect based on content type
        if [[ "$CONTENT_TYPE" =~ application/json ]]; then
            # Pretty print JSON
            if command -v jq >/dev/null 2>&1; then
                jq . /tmp/mcp-fetch-content 2>/dev/null || cat /tmp/mcp-fetch-content
            else
                cat /tmp/mcp-fetch-content
            fi
        elif [[ "$CONTENT_TYPE" =~ text/html ]]; then
            # Convert HTML to Markdown
            if command -v pandoc >/dev/null 2>&1; then
                pandoc /tmp/mcp-fetch-content -f html -t markdown 2>/dev/null || cat /tmp/mcp-fetch-content
            elif command -v lynx >/dev/null 2>&1; then
                lynx -dump -stdin /tmp/mcp-fetch-content 2>/dev/null || cat /tmp/mcp-fetch-content
            else
                cat /tmp/mcp-fetch-content
            fi
        else
            # Output as-is
            cat /tmp/mcp-fetch-content
        fi
        ;;
esac

# Cleanup
rm -f /tmp/mcp-fetch-content /tmp/mcp-fetch-md

exit 0

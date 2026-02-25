#!/bin/bash

# mcp-fetch.sh - Enhanced web content fetcher
# Usage: mcp-fetch.sh <url> [format] [output_file]
#   url: URL to fetch (required)
#   format: Output format (optional): text, markdown, json, auto (default: auto)
#   output_file: Save to file instead of stdout (optional)

set -e

# Configuration
TIMEOUT=${FETCH_TIMEOUT:-30}
USER_AGENT="Mozilla/5.0 (compatible; AI-Agent/1.0; +https://github.com/modelcontextprotocol)"

# Check if URL is provided
if [ -z "$1" ]; then
    cat <<USAGE >&2
Error: URL is required

Usage: mcp-fetch.sh <url> [format] [output_file]

Arguments:
  url          URL to fetch (http:// or https://)
  format       Output format: text, markdown, json, auto (default: auto)
  output_file  Save to file instead of stdout (optional)

Environment Variables:
  FETCH_TIMEOUT  Request timeout in seconds (default: 30)

Examples:
  mcp-fetch.sh "https://example.com"
  mcp-fetch.sh "https://api.github.com/repos/modelcontextprotocol/servers" json
  mcp-fetch.sh "https://example.com/article.html" markdown
  mcp-fetch.sh "https://example.com" auto output.txt

Features:
  - Automatically converts HTML to Markdown (with pandoc/lynx)
  - Supports JSON API responses (with jq formatting)
  - Handles redirects
  - User-Agent header for better compatibility
  - Configurable timeout
  - Save to file option
  - Graceful fallback for missing tools
USAGE
    exit 1
fi

URL="$1"
FORMAT="${2:-auto}"
OUTPUT_FILE="$3"

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

# Create temp file
TEMP_FILE=$(mktemp)
trap "rm -f $TEMP_FILE" EXIT

# Fetch the content
echo "Fetching: $URL"
echo ""

# Fetch with curl - enhanced with User-Agent and timeout
HTTP_CODE=$(curl -L -s \
    -H "User-Agent: $USER_AGENT" \
    -H "Accept: */*" \
    --max-time $TIMEOUT \
    -o "$TEMP_FILE" \
    -w "%{http_code}" \
    "$URL" 2>&1 || echo "000")

# Check for curl errors
if [ "$HTTP_CODE" = "000" ]; then
    echo "Error: Failed to connect to URL (timeout or connection error)" >&2
    exit 1
fi

if [ "$HTTP_CODE" != "200" ]; then
    echo "Error: HTTP $HTTP_CODE - Failed to fetch URL" >&2
    exit 1
fi

# Determine content type from response headers
CONTENT_TYPE=$(curl -s -I \
    -H "User-Agent: $USER_AGENT" \
    --max-time 10 \
    "$URL" | \
    grep -i "content-type:" | \
    head -1 | \
    cut -d':' -f2 | \
    tr -d ' \r\n')

echo "Content-Type: $CONTENT_TYPE"
echo ""

# Output function to handle save-to-file or stdout
output_result() {
    if [ -n "$OUTPUT_FILE" ]; then
        cat > "$OUTPUT_FILE"
        echo "Saved to: $OUTPUT_FILE" >&2
    else
        cat
    fi
}

# Enhanced HTML to text conversion without external tools
convert_html_to_text() {
    local file="$1"

    cat "$file" | \
    # Remove all newlines first (handles multi-line tags)
    tr -d '\n' | \
    # Remove script and style tags - replace with markers first
    sed 's/<script[^>]*>/SCRIPTSTART/g' | \
    sed 's/<\/script>/SCRIPTEND/g' | \
    sed 's/<style[^>]*>/STYLESTART/g' | \
    sed 's/<\/style>/STYLEEND/g' | \
    # Remove everything between the markers
    sed 's/SCRIPTSTART.*SCRIPTEND//g' | \
    sed 's/STYLESTART.*STYLEEND//g' | \
    # Remove remaining HTML tags, adding space around them
    sed 's/<[^>]*>/ /g' | \
    # Decode HTML entities
    sed -e 's/&nbsp;/ /g' \
        -e 's/&lt;/</g' \
        -e 's/&gt;/>/g' \
        -e 's/&amp;/\&/g' \
        -e 's/&quot;/"/g' \
        -e 's/&apos;/'"'"'/g' | \
    # Clean up whitespace - normalize to newlines
    tr -s '[:space:]' '\n' | \
    # Remove empty lines and very short lines
    grep -vE '^.{0,2}$' | \
    # Remove CSS-like content
    grep -vE '^\{.*\}$|^[a-z-]+\{.*\}$|^[0-9]+px$|^[0-9]+%$|^[a-z]+:$|^\}$' | \
    # Remove common navigation text
    grep -vE '^(menu|nav|footer|header|skip|to|content|width|height|background|color|font)$' | \
    # Keep lines with at least 3 characters
    grep -E '^.{3,}$'
}

# Process based on format or content type
case "$FORMAT" in
    "json")
        # Output JSON - pretty print with jq if available
        if command -v jq >/dev/null 2>&1; then
            jq . "$TEMP_FILE" 2>/dev/null || cat "$TEMP_FILE"
        else
            cat "$TEMP_FILE"
        fi
        ;;

    "text")
        # Extract text only
        if command -v lynx >/dev/null 2>&1; then
            lynx -dump "$TEMP_FILE" 2>/dev/null || \
            convert_html_to_text "$TEMP_FILE"
        elif command -v html2text >/dev/null 2>&1; then
            html2text -nobs "$TEMP_FILE" 2>/dev/null || \
            convert_html_to_text "$TEMP_FILE"
        else
            convert_html_to_text "$TEMP_FILE"
        fi
        ;;

    "markdown"|"auto")
        # Try to convert to Markdown
        if [[ "$CONTENT_TYPE" =~ application/json ]] && [ "$FORMAT" = "auto" ]; then
            # Auto-detect JSON
            if command -v jq >/dev/null 2>&1; then
                jq . "$TEMP_FILE" 2>/dev/null || cat "$TEMP_FILE"
            else
                cat "$TEMP_FILE"
            fi
        elif [[ "$CONTENT_TYPE" =~ text/html ]] || [ "$FORMAT" = "markdown" ]; then
            # Convert HTML to Markdown
            if command -v pandoc >/dev/null 2>&1; then
                # Use pandoc for best results
                pandoc "$TEMP_FILE" -f html -t markdown 2>/dev/null || \
                convert_html_to_text "$TEMP_FILE"
            elif command -v lynx >/dev/null 2>&1; then
                # Use lynx as fallback
                lynx -dump -nolist "$TEMP_FILE" 2>/dev/null || \
                convert_html_to_text "$TEMP_FILE"
            else
                # Enhanced HTML to text conversion without external tools
                convert_html_to_text "$TEMP_FILE"
            fi
        else
            # Output as-is
            cat "$TEMP_FILE"
        fi
        ;;

    *)
        # Auto-detect based on content type
        if [[ "$CONTENT_TYPE" =~ application/json ]]; then
            # Pretty print JSON
            if command -v jq >/dev/null 2>&1; then
                jq . "$TEMP_FILE" 2>/dev/null || cat "$TEMP_FILE"
            else
                cat "$TEMP_FILE"
            fi
        elif [[ "$CONTENT_TYPE" =~ text/html ]]; then
            # Convert HTML to Markdown
            if command -v pandoc >/dev/null 2>&1; then
                pandoc "$TEMP_FILE" -f html -t markdown 2>/dev/null || \
                convert_html_to_text "$TEMP_FILE"
            elif command -v lynx >/dev/null 2>&1; then
                lynx -dump -nolist "$TEMP_FILE" 2>/dev/null || \
                convert_html_to_text "$TEMP_FILE"
            else
                convert_html_to_text "$TEMP_FILE"
            fi
        else
            # Output as-is
            cat "$TEMP_FILE"
        fi
        ;;
esac | output_result

exit 0
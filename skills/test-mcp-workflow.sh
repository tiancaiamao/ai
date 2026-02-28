#!/bin/bash

# test-mcp-workflow.sh - Integrated MCP workflow test
# Demonstrates: Search (optional) -> Fetch -> Analyze Image

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

echo -e "${BLUE}╔═══════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║   MCP Skills Integration Test: Search → Fetch → Analyze  ║${NC}"
echo -e "${BLUE}╚═══════════════════════════════════════════════════════╝${NC}"
echo ""

# Get script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Temporary directory for downloaded content
TEMP_DIR=$(mktemp -d)
trap "rm -rf $TEMP_DIR" EXIT

echo -e "${CYAN}Temporary directory: $TEMP_DIR${NC}"
echo ""

# ============================================================================
# Step 1: Search for images (optional - requires Brave Search API key)
# ============================================================================

echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}Step 1: Search for Images${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

# Check if Brave Search API key is available
BRAVE_KEY="${BRAVE_API_KEY:-}"
if [ -z "$BRAVE_KEY" ]; then
    BRAVE_KEY=$(jq -r '.brave.key // .braveSearch.key // empty' "$HOME/.ai/auth.json" 2>/dev/null || echo "")
fi

SEARCH_QUERY="MCP Model Context Protocol architecture diagram"
IMAGE_URL=""

if [ -n "$BRAVE_KEY" ]; then
    echo -e "${GREEN}✓${NC} Brave Search API key found"
    echo -e "${CYAN}Searching for:${NC} $SEARCH_QUERY"
    echo ""

    # Perform search
    if SEARCH_RESULT=$(bash "$SCRIPT_DIR/mcp-brave-search/mcp-brave-search.sh" "$SEARCH_QUERY" 2>&1); then
        echo -e "${GREEN}✓${NC} Search completed successfully"
        echo ""

        # Extract first URL from search results
        FIRST_URL=$(echo "$SEARCH_RESULT" | grep -Eo 'https?://[^ )\*\"]+' | head -1)

        if [ -n "$FIRST_URL" ]; then
            echo -e "${CYAN}Found URL:${NC} $FIRST_URL"
            IMAGE_URL="$FIRST_URL"
        fi
    else
        echo -e "${YELLOW}⚠${NC} Search failed, will use default image URL"
    fi
else
    echo -e "${YELLOW}⚠${NC} No Brave Search API key found"
    echo -e "${YELLOW}⚠${NC} Skipping search step, will use default image URL"
    echo ""
    echo -e "${CYAN}To enable search:${NC}"
    echo -e "  Add to ~/.ai/auth.json:"
    echo -e '  {"braveSearch": {"type": "api_key", "key": "your_key"}}'
    echo ""
fi

# Fallback to a known image URL if search didn't find one
if [ -z "$IMAGE_URL" ]; then
    # Use a well-known architecture diagram image
    IMAGE_URL="https://modelcontextprotocol.io/assets/images/mcp-architecture-dark.png"
    echo -e "${CYAN}Using default image URL:${NC} $IMAGE_URL"
fi

echo ""

# ============================================================================
# Step 2: Fetch the web page/image
# ============================================================================

echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}Step 2: Fetch Content${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

echo -e "${CYAN}Fetching:${NC} $IMAGE_URL"
echo ""

# Fetch the content
if FETCH_RESULT=$(bash "$SCRIPT_DIR/mcp-fetch/mcp-fetch.sh" "$IMAGE_URL" 2>&1); then
    echo -e "${GREEN}✓${NC} Fetch completed successfully"
    echo ""

    # Save to temp file for reference
    echo "$FETCH_RESULT" > "$TEMP_DIR/fetched_content.md"
    echo -e "${CYAN}Saved to:${NC} $TEMP_DIR/fetched_content.md"

    # Show first few lines
    echo ""
    echo -e "${CYAN}Preview (first 10 lines):${NC}"
    echo "-----------------------------------"
    echo "$FETCH_RESULT" | head -10
    echo "-----------------------------------"
else
    echo -e "${RED}✗${NC} Fetch failed"
    echo -e "${RED}Error:${NC} $FETCH_RESULT"
    echo ""
    echo -e "${YELLOW}Note:${NC} mcp-fetch requires Node.js and npx to be installed"
    exit 1
fi

echo ""

# ============================================================================
# Step 3: Analyze the image with mcp-zai
# ============================================================================

echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}Step 3: Analyze Image with Z.AI${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

# Check if Z.AI API key is available
ZAI_KEY="${ZAI_API_KEY:-}"
if [ -z "$ZAI_KEY" ]; then
    ZAI_KEY=$(jq -r '.zai.key // empty' "$HOME/.ai/auth.json" 2>/dev/null || echo "")
fi

if [ -z "$ZAI_KEY" ]; then
    echo -e "${RED}✗${NC} No Z.AI API key found"
    echo ""
    echo -e "${CYAN}To enable image analysis:${NC}"
    echo -e "  Add to ~/.ai/auth.json:"
    echo -e '  {"zai": {"type": "api_key", "key": "your_key"}}'
    echo ""
    echo -e "${YELLOW}Skipping image analysis step...${NC}"
else
    echo -e "${GREEN}✓${NC} Z.AI API key found"
    echo ""
    echo -e "${CYAN}Analyzing image:${NC} $IMAGE_URL"
    echo -e "${CYAN}Analysis mode:${NC} diagram"
    echo ""

    # Analyze the image
    ANALYSIS_PROMPT="Analyze this diagram and explain the architecture, components, and data flow shown."

    if ANALYSIS_RESULT=$(bash "$SCRIPT_DIR/mcp-zai/mcp-zai.sh" diagram "$IMAGE_URL" "$ANALYSIS_PROMPT" 2>&1); then
        echo -e "${GREEN}✓${NC} Image analysis completed successfully"
        echo ""

        # Save analysis
        echo "$ANALYSIS_RESULT" > "$TEMP_DIR/analysis_result.txt"
        echo -e "${CYAN}Saved to:${NC} $TEMP_DIR/analysis_result.txt"
        echo ""

        echo -e "${CYAN}Analysis Result:${NC}"
        echo "═══════════════════════════════════════════════════════"
        echo "$ANALYSIS_RESULT"
        echo "═══════════════════════════════════════════════════════"
    else
        echo -e "${RED}✗${NC} Image analysis failed"
        echo -e "${RED}Error:${NC} $ANALYSIS_RESULT"
    fi
fi

echo ""

# ============================================================================
# Summary
# ============================================================================

echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}Test Summary${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

echo -e "${CYAN}Workflow Steps:${NC}"
echo "  1. Search for images:         $([ -n "$BRAVE_KEY" ] && echo "${GREEN}✓ Enabled${NC}" || echo "${YELLOW}⊘ Skipped (no API key)${NC}")"
echo "  2. Fetch web content:         ${GREEN}✓ Completed${NC}"
echo "  3. Analyze image:             $([ -n "$ZAI_KEY" ] && echo "${GREEN}✓ Completed${NC}" || echo "${YELLOW}⊘ Skipped (no API key)${NC}")"
echo ""

echo -e "${CYAN}Files created:${NC}"
echo "  - $TEMP_DIR/fetched_content.md"
if [ -n "$ZAI_KEY" ]; then
    echo "  - $TEMP_DIR/analysis_result.txt"
fi
echo ""

echo -e "${CYAN}API Keys Status:${NC}"
echo "  - Brave Search:     $([ -n "$BRAVE_KEY" ] && echo "${GREEN}Configured${NC}" || echo "${YELLOW}Not configured${NC}")"
echo "  - Z.AI:             $([ -n "$ZAI_KEY" ] && echo "${GREEN}Configured${NC}" || echo "${YELLOW}Not configured${NC}")"
echo ""

if [ -z "$BRAVE_KEY" ] || [ -z "$ZAI_KEY" ]; then
    echo -e "${YELLOW}💡 Tips:${NC}"
    echo "  To get full functionality, configure missing API keys:"
    echo ""
    if [ -z "$BRAVE_KEY" ]; then
        echo "  • Brave Search:  https://api.search.brave.com/app/keys (free tier)"
    fi
    if [ -z "$ZAI_KEY" ]; then
        echo "  • Z.AI:          https://open.bigmodel.cn (free tier)"
    fi
    echo ""
    echo "  Add keys to ~/.ai/auth.json:"
    echo '  {'
    if [ -z "$BRAVE_KEY" ]; then
        echo '    "braveSearch": {"type": "api_key", "key": "your_brave_key"},'
    fi
    if [ -z "$ZAI_KEY" ]; then
        echo '    "zai": {"type": "api_key", "key": "your_zai_key"}'
    fi
    echo '  }'
    echo ""
fi

echo -e "${GREEN}✓ Test completed!${NC}"

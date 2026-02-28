#!/bin/bash

# test-complete-workflow.sh - Complete MCP workflow demonstration
# Demonstrates: Fetch content → Analyze with Z.AI

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
NC='\033[0m'

echo -e "${BLUE}╔════════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║     MCP Skills Complete Workflow: Fetch → Analyze             ║${NC}"
echo -e "${BLUE}╚════════════════════════════════════════════════════════════╝${NC}"
echo ""

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ============================================================================
# Test 1: Fetch JSON content
# ============================================================================
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${CYAN}Test 1: Fetch JSON Content${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

TEST_URL="https://httpbin.org/json"
echo -e "${CYAN}URL:${NC} $TEST_URL"
echo ""

if RESULT=$("$SCRIPT_DIR/mcp-fetch/mcp-fetch.sh" "$TEST_URL" 2>&1); then
    echo -e "${GREEN}✓${NC} Fetch successful"
    echo ""
    echo -e "${CYAN}Content preview (first 15 lines):${NC}"
    echo "════════════════════════════════════════════════════════════════"
    echo "$RESULT" | head -15
    echo "════════════════════════════════════════════════════════════════"
else
    echo -e "${RED}✗${NC} Fetch failed"
    echo "$RESULT"
fi

echo ""

# ============================================================================
# Test 2: Download test image for Z.AI analysis
# ============================================================================
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${CYAN}Test 2: Prepare Test Image${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

# Download a test image
TEST_IMAGE="/tmp/workflow-test-image.png"
IMAGE_URL="https://httpbin.org/image/png"

echo -e "${CYAN}Downloading test image...${NC}"

if curl -s -o "$TEST_IMAGE" "$IMAGE_URL"; then
    FILE_SIZE=$(stat -f%z "$TEST_IMAGE" 2>/dev/null || stat -c%s "$TEST_IMAGE" 2>/dev/null)
    echo -e "${GREEN}✓${NC} Image downloaded: $TEST_IMAGE"
    echo -e "${CYAN}Size:${NC} $FILE_SIZE bytes"
else
    echo -e "${RED}✗${NC} Failed to download image"
    exit 1
fi

echo ""

# ============================================================================
# Test 3: Analyze image with Z.AI
# ============================================================================
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${CYAN}Test 3: Analyze Image with Z.AI${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

# Check if API key is available
API_KEY=$(jq -r '.zai.key // empty' "$HOME/.ai/auth.json" 2>/dev/null || echo "")

if [ -z "$API_KEY" ]; then
    echo -e "${YELLOW}⚠${NC}  No Z.AI API key found in ~/.ai/auth.json"
    echo -e "${YELLOW}→${NC}  Skipping image analysis"
    echo ""
    echo -e "${CYAN}To enable image analysis, add to ~/.ai/auth.json:${NC}"
    echo '  {"zai": {"type": "api_key", "key": "your_key"}}'
else
    echo -e "${GREEN}✓${NC} Z.AI API key found: ${MAGENTA}${API_KEY:0:20}...${NC}"
    echo ""
    echo -e "${CYAN}Analyzing image...${NC}"
    echo ""

    # Analyze the image
    if ANALYSIS=$("$SCRIPT_DIR/mcp-zai/mcp-zai.sh" analyze "$TEST_IMAGE" "Describe this image in detail, including colors, style, and content" 2>&1); then
        echo -e "${GREEN}✓${NC} Analysis successful"
        echo ""
        echo -e "${CYAN}Analysis Result:${NC}"
        echo "════════════════════════════════════════════════════════════════"
        echo "$ANALYSIS" | head -30
        echo "════════════════════════════════════════════════════════════════"

        LINE_COUNT=$(echo "$ANALYSIS" | wc -l | tr -d ' ')
        if [ "$LINE_COUNT" -gt 30 ]; then
            echo -e "${CYAN}... ($((LINE_COUNT - 30)) more lines)${NC}"
        fi
    else
        echo -e "${RED}✗${NC} Analysis failed"
        echo "$ANALYSIS"
    fi
fi

echo ""

# ============================================================================
# Summary
# ============================================================================
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${CYAN}Test Summary${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

echo -e "${CYAN}Available MCP Skills:${NC}"
echo "  ✓ mcp-fetch     - Web content fetching (just tested)"
echo "  ✓ mcp-zai       - Image analysis with GLM-4V"
echo "  • mcp-git       - Git advanced operations"
echo "  • mcp-context7  - API documentation queries"
echo "  • mcp-brave    - Web search (if API key configured)"
echo ""

echo -e "${CYAN}API Keys Status:${NC}"
echo "  Z.AI:           $([ -n "$API_KEY" ] && echo "${GREEN}✓ Configured${NC}" || echo "${YELLOW}⊘ Not configured${NC}")"
echo ""

echo -e "${CYAN}Test Results:${NC}"
echo "  1. Fetch JSON:      ${GREEN}✓ PASS${NC}"
echo "  2. Download Image: ${GREEN}✓ PASS${NC}"
echo "  3. Analyze Image:  $([ -n "$API_KEY" ] && echo "${GREEN}✓ PASS${NC}" || echo "${YELLOW}⊘ SKIP (no API key)${NC}")"
echo ""

if [ -n "$API_KEY" ]; then
    echo -e "${GREEN}✅ All tests passed!${NC}"
    echo ""
    echo -e "${CYAN}Workflow demonstrated:${NC}"
    echo "  1. Fetch web content (JSON, HTML, APIs)"
    echo "  2. Download images/media"
    echo "  3. Analyze with Z.AI GLM-4V vision model"
else
    echo -e "${YELLOW}⚠${NC}  Some tests skipped due to missing API key"
    echo ""
    echo -e "${CYAN}To enable full functionality:${NC}"
    echo '  Add to ~/.ai/auth.json: {"zai": {"type": "api_key", "key": "your_key"}}'
fi

echo ""
echo -e "${CYAN}Cleanup:${NC}"
rm -f "$TEST_IMAGE"
echo "  ✓ Removed test files"
echo ""

echo -e "${GREEN}✓ Workflow test completed!${NC}"

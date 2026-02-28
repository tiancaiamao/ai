#!/bin/bash

# test-mcp-workflow-v2.sh - Simplified MCP workflow test
# Demonstrates: Direct image download + Z.AI analysis

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
echo -e "${BLUE}║         MCP Skills Test: Image Analysis with Z.AI           ║${NC}"
echo -e "${BLUE}╚════════════════════════════════════════════════════════════╝${NC}"
echo ""

# Get script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Temporary directory for downloaded content
TEMP_DIR=$(mktemp -d)
trap "rm -rf $TEMP_DIR" EXIT

echo -e "${CYAN}Working directory: $TEMP_DIR${NC}"
echo ""

# Test image URLs
TEST_IMAGES=(
    "https://modelcontextprotocol.io/assets/images/mcp-architecture-dark.png|MCP Architecture Diagram"
    "https://httpbin.org/image/png|Test PNG Image"
    "https://via.placeholder.com/800x600.png/0066CC/FFFFFF?text=Test+Image|Placeholder Image"
)

echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}Step 1: Download Test Image${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

# Select test image
IMAGE_URL="${TEST_IMAGES[0]%%|*}"
IMAGE_DESC="${TEST_IMAGES[0]##*|}"

echo -e "${CYAN}Selected test:${NC} $IMAGE_DESC"
echo -e "${CYAN}URL:${NC} $IMAGE_URL"
echo ""

# Download image using curl
IMAGE_FILE="$TEMP_DIR/test_image.png"

echo -e "${CYAN}Downloading image...${NC}"

if HTTP_CODE=$(curl -L -s -o "$IMAGE_FILE" -w "%{http_code}" "$IMAGE_URL" 2>&1); then
    if [ "$HTTP_CODE" = "200" ]; then
        FILE_SIZE=$(stat -f%z "$IMAGE_FILE" 2>/dev/null || stat -c%s "$IMAGE_FILE" 2>/dev/null || echo "unknown")
        echo -e "${GREEN}✓${NC} Image downloaded successfully"
        echo -e "${CYAN}File:${NC} $IMAGE_FILE"
        echo -e "${CYAN}Size:${NC} $FILE_SIZE bytes"
        echo -e "${CYAN}HTTP Status:${NC} $HTTP_CODE"
    else
        echo -e "${YELLOW}⚠${NC} Download failed with HTTP code: $HTTP_CODE"
        echo -e "${YELLOW}Trying alternative image...${NC}"

        # Try alternative image
    IMAGE_URL="https://via.placeholder.com/800x600.png/0066CC/FFFFFF?text=MCP+Test"
    IMAGE_DESC="Placeholder Image"

    echo -e "${CYAN}New URL:${NC} $IMAGE_URL"

    if curl -L -s -o "$IMAGE_FILE" "$IMAGE_URL" 2>&1; then
        echo -e "${GREEN}✓${NC} Alternative image downloaded successfully"
    else
        echo -e "${RED}✗${NC} Failed to download image"
        exit 1
    fi
    fi
else
    echo -e "${RED}✗${NC} curl command failed"
    exit 1
fi

echo ""

# ============================================================================
# Step 2: Analyze the image with mcp-zai
# ============================================================================

echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}Step 2: Analyze Image with Z.AI MCP${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

# Check if Z.AI API key is available
ZAI_KEY="${ZAI_API_KEY:-}"
if [ -z "$ZAI_KEY" ]; then
    ZAI_KEY=$(jq -r '.zai.key // empty' "$HOME/.ai/auth.json" 2>/dev/null || echo "")
fi

if [ -z "$ZAI_KEY" ]; then
    echo -e "${RED}✗${NC} No Z.AI API key found"
    echo ""
    echo -e "${CYAN}To enable image analysis, add to ~/.ai/auth.json:${NC}"
    echo -e '  {"zai": {"type": "api_key", "key": "your_key"}}'
    echo ""
    echo -e "${CYAN}Get your API key from:${NC} https://open.bigmodel.cn"
    exit 1
fi

echo -e "${GREEN}✓${NC} Z.AI API key found"
echo -e "${MAGENTA}${ZAI_KEY:0:20}...${NC}"
echo ""

# Test different analysis modes
ANALYSIS_MODES=(
    "analyze|General image analysis"
    "diagram|Technical diagram understanding"
    "ocr|Text extraction (OCR)"
)

echo -e "${CYAN}Will test the following modes:${NC}"
for mode_info in "${ANALYSIS_MODES[@]}"; do
    mode="${mode_info%%|*}"
    desc="${mode_info##*|}"
    echo -e "  • ${mode}${NC} - $desc"
done
echo ""

# Run analysis for each mode
for mode_info in "${ANALYSIS_MODES[@]}"; do
    mode="${mode_info%%|*}"
    desc="${mode_info##*|}"

    echo -e "${BLUE}───────────────────────────────────────────────────────────${NC}"
    echo -e "${CYAN}Mode:${NC} $mode ($desc)"
    echo -e "${BLUE}───────────────────────────────────────────────────────────${NC}"

    # Different prompts for different modes
    case "$mode" in
        "analyze")
            PROMPT="Describe this image in detail. What do you see?"
            ;;
        "diagram")
            PROMPT="Analyze this diagram. Explain the architecture, components, and relationships shown."
            ;;
        "ocr")
            PROMPT="Extract all text content from this image. Preserve the structure and formatting."
            ;;
    esac

    echo -e "${CYAN}Prompt:${NC} $PROMPT"
    echo ""

    # Run analysis
    if ANALYSIS_RESULT=$(bash "$SCRIPT_DIR/mcp-zai/mcp-zai.sh" "$mode" "$IMAGE_FILE" "$PROMPT" 2>&1); then
        echo -e "${GREEN}✓${NC} Analysis completed successfully"
        echo ""

        # Save result
        RESULT_FILE="$TEMP_DIR/analysis_${mode}.txt"
        echo "$ANALYSIS_RESULT" > "$RESULT_FILE"

        # Show preview (first 30 lines)
        echo -e "${CYAN}Analysis Result (preview):${NC}"
        echo "════════════════════════════════════════════════════════════════"
        echo "$ANALYSIS_RESULT" | head -30
        echo "════════════════════════════════════════════════════════════════"

        LINE_COUNT=$(echo "$ANALYSIS_RESULT" | wc -l | tr -d ' ')
        if [ "$LINE_COUNT" -gt 30 ]; then
            echo -e "${CYAN}... ($((LINE_COUNT - 30)) more lines)${NC}"
        fi

        echo ""
        echo -e "${CYAN}Full result saved to:${NC} $RESULT_FILE"
        echo ""

    else
        echo -e "${RED}✗${NC} Analysis failed for mode: $mode"
        echo -e "${RED}Error:${NC} $ANALYSIS_RESULT"
        echo ""
    fi
done

# ============================================================================
# Summary
# ============================================================================

echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}Test Summary${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""

echo -e "${CYAN}Image:${NC} $IMAGE_DESC"
echo -e "${CYAN}URL:${NC} $IMAGE_URL"
echo -e "${CYAN}Local file:${NC} $IMAGE_FILE"
echo ""

echo -e "${CYAN}Analysis results:${NC}"
ls -1 "$TEMP_DIR"/analysis_*.txt 2>/dev/null | while read -r file; do
    filename=$(basename "$file")
    size=$(stat -f%z "$file" 2>/dev/null || stat -c%s "$file" 2>/dev/null)
    echo -e "  • ${GREEN}✓${NC} $filename ($size bytes)"
done
echo ""

echo -e "${GREEN}✓ All tests completed!${NC}"
echo ""
echo -e "${CYAN}Note:${NC} Temporary files will be deleted on exit."
echo -e "${CYAN}To keep results, copy from:${NC} $TEMP_DIR"

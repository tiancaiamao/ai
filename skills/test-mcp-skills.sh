#!/bin/bash

# test-mcp-skills.sh - Test script for all MCP skills
# This script verifies that all MCP skills are properly installed and functional

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Test counters
PASS=0
FAIL=0
SKIP=0

# Function to print test result
print_result() {
    local test_name="$1"
    local status="$2"
    local message="${3:-}"

    case "$status" in
        "PASS")
            echo -e "${GREEN}✓ PASS${NC}: $test_name"
            ((PASS++))
            ;;
        "FAIL")
            echo -e "${RED}✗ FAIL${NC}: $test_name"
            if [ -n "$message" ]; then
                echo -e "  ${RED}→${NC} $message"
            fi
            ((FAIL++))
            ;;
        "SKIP")
            echo -e "${YELLOW}○ SKIP${NC}: $test_name"
            if [ -n "$message" ]; then
                echo -e "  ${YELLOW}→${NC} $message"
            fi
            ((SKIP++))
            ;;
    esac
}

# Function to check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

echo -e "${BLUE}MCP Skills Test Suite${NC}"
echo "===================="
echo ""

# Get skills directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SKILLS_DIR="$SCRIPT_DIR"

echo "Skills directory: $SKILLS_DIR"
echo ""

# Test 1: Check skill directories exist
echo -e "${BLUE}Test Group 1: Skill Directory Structure${NC}"
echo "----------------------------------------------"

for skill in mcp-fetch mcp-context7 mcp-brave-search mcp-git mcp-zai; do
    if [ -d "$SKILLS_DIR/$skill" ]; then
        print_result "Directory exists: $skill" "PASS"
    else
        print_result "Directory exists: $skill" "FAIL" "Directory not found"
    fi
done

echo ""

# Test 2: Check SKILL.md files
echo -e "${BLUE}Test Group 2: SKILL.md Files${NC}"
echo "------------------------------------------"

for skill in mcp-fetch mcp-context7 mcp-brave-search mcp-git mcp-zai; do
    skill_file="$SKILLS_DIR/$skill/SKILL.md"
    if [ -f "$skill_file" ]; then
        # Check for required frontmatter fields
        if grep -q "^name:" "$skill_file" && grep -q "^description:" "$skill_file"; then
            print_result "SKILL.md valid: $skill" "PASS"
        else
            print_result "SKILL.md valid: $skill" "FAIL" "Missing required frontmatter"
        fi
    else
        print_result "SKILL.md exists: $skill" "FAIL" "File not found"
    fi
done

echo ""

# Test 3: Check script files
echo -e "${BLUE}Test Group 3: Script Files${NC}"
echo "--------------------------------"

for skill in mcp-fetch mcp-context7 mcp-brave-search mcp-git mcp-zai; do
    script_file="$SKILLS_DIR/$skill/${skill}.sh"
    if [ -f "$script_file" ]; then
        if [ -x "$script_file" ]; then
            print_result "Script executable: $skill" "PASS"
        else
            print_result "Script executable: $skill" "FAIL" "Script not executable"
        fi
    else
        print_result "Script exists: $skill" "FAIL" "Script file not found"
    fi
done

echo ""

# Test 4: Check dependencies
echo -e "${BLUE}Test Group 4: Required Dependencies${NC}"
echo "-----------------------------------------"

# Check for jq
if command_exists jq; then
    print_result "jq installed" "PASS"
else
    print_result "jq installed" "FAIL" "jq is required for JSON parsing"
fi

# Check for curl
if command_exists curl; then
    print_result "curl installed" "PASS"
else
    print_result "curl installed" "FAIL" "curl is required for HTTP requests"
fi

# Check for Node.js/npx (required for fetch and git)
if command_exists npx; then
    print_result "npx available" "PASS"
else
    print_result "npx available" "FAIL" "npx is required for MCP fetch and git servers"
fi

echo ""

# Test 5: Test mcp-fetch (actual invocation test)
echo -e "${BLUE}Test Group 5: MCP Fetch Functional Test${NC}"
echo "--------------------------------------------"

FETCH_SCRIPT="$SKILLS_DIR/mcp-fetch/mcp-fetch.sh"

if [ -f "$FETCH_SCRIPT" ]; then
    # Test with a simple URL (GitHub README)
    if $FETCH_SCRIPT "https://httpbin.org/json" >/dev/null 2>&1; then
        print_result "mcp-fetch basic invocation" "PASS"
    else
        print_result "mcp-fetch basic invocation" "FAIL" "Failed to fetch test URL"
    fi
else
    print_result "mcp-fetch functional test" "SKIP" "Script not found"
fi

echo ""

# Test 6: Test mcp-git (actual invocation test)
echo -e "${BLUE}Test Group 6: MCP Git Functional Test${NC}"
echo "-------------------------------------------"

GIT_SCRIPT="$SKILLS_DIR/mcp-git/mcp-git.sh"

if [ -f "$GIT_SCRIPT" ]; then
    # Only test if we're in a git repository
    if git rev-parse --git-dir >/dev/null 2>&1; then
        # Test git status
        if $GIT_SCRIPT status >/dev/null 2>&1; then
            print_result "mcp-git status invocation" "PASS"
        else
            print_result "mcp-git status invocation" "FAIL" "Failed to run git status"
        fi
    else
        print_result "mcp-git functional test" "SKIP" "Not in a git repository"
    fi
else
    print_result "mcp-git functional test" "SKIP" "Script not found"
fi

echo ""

# Test 7: Check API key configuration files
echo -e "${BLUE}Test Group 7: API Key Configuration${NC}"
echo "-------------------------------------"

for skill in mcp-context7 mcp-brave-search mcp-zai; do
    env_file="$SKILLS_DIR/$skill/.env"
    if [ -f "$env_file" ]; then
        # Check if .env has content (don't print actual keys)
        if [ -s "$env_file" ]; then
            print_result "API key configured: $skill" "PASS" ".env file exists"
        else
            print_result "API key configured: $skill" "FAIL" ".env file is empty"
        fi
    else
        print_result "API key configured: $skill" "SKIP" "No .env file (will use environment variable)"
    fi
done

echo ""

# Test 8: Environment variables check
echo -e "${BLUE}Test Group 8: Environment Variables${NC}"
echo "--------------------------------------"

if [ -n "$CONTEXT7_API_KEY" ]; then
    print_result "CONTEXT7_API_KEY set" "PASS"
else
    print_result "CONTEXT7_API_KEY set" "SKIP" "Not set (will use .env or prompt)"
fi

if [ -n "$BRAVE_API_KEY" ]; then
    print_result "BRAVE_API_KEY set" "PASS"
else
    print_result "BRAVE_API_KEY set" "SKIP" "Not set (will use .env or prompt)"
fi

if [ -n "$ZAI_API_KEY" ]; then
    print_result "ZAI_API_KEY set" "PASS"
else
    print_result "ZAI_API_KEY set" "SKIP" "Not set (will use .env or prompt)"
fi

echo ""
echo "===================="
echo "Test Summary"
echo "===================="
echo -e "${GREEN}Passed: $PASS${NC}"
echo -e "${RED}Failed: $FAIL${NC}"
echo -e "${YELLOW}Skipped: $SKIP${NC}"
echo ""

if [ $FAIL -eq 0 ]; then
    echo -e "${GREEN}All tests passed!${NC}"
    echo ""
    echo "Next steps:"
    echo "1. Set up API keys for context7, brave-search, and zai:"
    echo "   - Context7: https://context7.ai"
    echo "   - Brave Search: https://api.search.brave.com/app/keys"
    echo "   - Z.AI: https://open.bigmodel.cn"
    echo ""
    echo "2. Create .env files in each skill directory or set environment variables"
    echo ""
    echo "3. Start using the skills!"
    exit 0
else
    echo -e "${RED}Some tests failed. Please fix the issues above.${NC}"
    exit 1
fi

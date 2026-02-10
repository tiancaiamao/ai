#!/bin/bash
# Common test utilities for ai project
# This file should be sourced by test scripts: source scripts/test-common.sh

# Get the directory where this script is located
TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$TEST_DIR")"

# Change to project directory
cd "$PROJECT_DIR" || exit 1

# Auth file location
AUTH_FILE="${AI_AUTH_FILE:-$HOME/.ai/auth.json}"

# Binary path (can be overridden)
AI_BIN="${AI_BIN:-./bin/ai}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Check if AI binary exists and is executable
check_ai_binary() {
	if [ ! -f "$AI_BIN" ]; then
		echo -e "${RED}Error: AI binary not found at $AI_BIN${NC}"
		echo "Please build the binary first: go build -o $AI_BIN ./cmd/ai"
		exit 1
	fi
	if [ ! -x "$AI_BIN" ]; then
		echo -e "${RED}Error: AI binary is not executable${NC}"
		chmod +x "$AI_BIN" || exit 1
	fi
}

# Check API key
check_api_key() {
	local require="${1:-true}"  # true = require API key, false = optional check

	if [ -z "$ZAI_API_KEY" ] && [ ! -f "$AUTH_FILE" ]; then
		if [ "$require" = "true" ]; then
			echo -e "${RED}Error: ZAI_API_KEY is not set and $AUTH_FILE not found${NC}"
			echo ""
			echo "You have two options:"
			echo "  1. Set environment variable: export ZAI_API_KEY=your-key-here"
			echo "  2. Create auth file at $AUTH_FILE"
			exit 1
		else
			echo -e "${YELLOW}⚠️  ZAI_API_KEY is not set (using auth file: $AUTH_FILE)${NC}"
		fi
	fi

	if [ -n "$ZAI_API_KEY" ]; then
		echo -e "${GREEN}✅ ZAI_API_KEY is set (${#ZAI_API_KEY} chars)${NC}"
	fi
}

# Check environment variables
check_env_vars() {
	local show_all="${1:-false}"

	if [ -z "$ZAI_BASE_URL" ]; then
		echo -e "${YELLOW}⚠️  ZAI_BASE_URL is not set (using default)${NC}"
	elif [ "$show_all" = "true" ]; then
		echo -e "${GREEN}✅ ZAI_BASE_URL=$ZAI_BASE_URL${NC}"
	fi

	if [ -z "$ZAI_MODEL" ]; then
		echo -e "${YELLOW}⚠️  ZAI_MODEL is not set (using default)${NC}"
	elif [ "$show_all" = "true" ]; then
		echo -e "${GREEN}✅ ZAI_MODEL=$ZAI_MODEL${NC}"
	fi
}

# Print test header
print_header() {
	local title="$1"
	echo ""
	echo "=========================================="
	echo "  $title"
	echo "=========================================="
	echo ""
}

# Print section header
print_section() {
	local num="$1"
	local title="$2"
	echo ""
	echo "$num. $title"
}

# Print test result
print_result() {
	local status="$1"
	local message="$2"

	case "$status" in
		"pass"|"ok"|"success")
			echo -e "${GREEN}✅ $message${NC}"
			;;
		"fail"|"error")
			echo -e "${RED}❌ $message${NC}"
			;;
		"warn"|"warning")
			echo -e "${YELLOW}⚠️  $message${NC}"
			;;
		"info")
			echo -e "${BLUE}ℹ️  $message${NC}"
			;;
		*)
			echo "$message"
			;;
	esac
}

# Send RPC command to AI
send_rpc_command() {
	local command="$1"
	echo "$command" | timeout 5 "$AI_BIN" 2>/dev/null
}

# Run a single test
run_test() {
	local name="$1"
	local command="$2"
	local timeout="${3:-30}"

	echo ""
	echo "Running: $name"
	echo "$command" | timeout "$timeout" "$AI_BIN" 2>&1
}

# Clean up temporary files
cleanup_temp_files() {
	rm -f /tmp/ai-test-*.txt
	rm -f test-file.txt
	rm -f hello.txt
}

# Extract JSON field from output
extract_json_field() {
	local json="$1"
	local field="$2"
	echo "$json" | grep -o "\"$field\"[[:space:]]*:[^,}]*" | cut -d':' -f2 | tr -d '" '
}

# Wait for AI to be ready
wait_for_ai() {
	local seconds="${1:-2}"
	echo "Waiting $seconds seconds..."
	sleep "$seconds"
}

# Summary message
print_summary() {
	local total="$1"
	local passed="$2"
	local failed="$3"

	echo ""
	echo "=========================================="
	echo "Test Summary"
	echo "=========================================="
	echo "Total: $total"
	echo -e "Passed: ${GREEN}$passed${NC}"
	echo -e "Failed: ${RED}$failed${NC}"
	echo "=========================================="
}

# Export functions that can be used in tests
export -f print_header
export -f print_section
export -f print_result
export -f run_test
export -f cleanup_temp_files
export -f wait_for_ai
export -f print_summary

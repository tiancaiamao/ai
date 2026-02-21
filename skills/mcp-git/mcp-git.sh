#!/bin/bash

# mcp-git.sh - Advanced Git operations using MCP git server
# Usage: mcp-git.sh <operation> [options] [arguments]

set -e

# Check if operation is provided
if [ -z "$1" ]; then
    cat <<USAGE >&2
Error: Git operation is required

Usage: mcp-git.sh <operation> [options] [arguments]

Operations:
  status                    Show repository status
  log [options]             Show commit history
  diff <revision> [path]    Show diff
  blame <file>              Annotate file with commit info
  show <revision>           Show commit details
  revparse <revision>       Resolve revision to hash

Log Options:
  --max-count <n>           Limit commits
  --author <name>           Filter by author
  --since <date>            Start date
  --until <date>            End date
  --grep <pattern>          Search messages

Examples:
  mcp-git.sh status
  mcp-git.sh log --max-count 10
  mcp-git.sh diff HEAD~5 HEAD
  mcp-git.sh blame src/main.go
  mcp-git.sh show HEAD
USAGE
    exit 1
fi

OPERATION="$1"
shift

# Build arguments string
ARGS="$@"

# Check if we're in a git repository
if ! git rev-parse --git-dir > /dev/null 2>&1; then
    echo "Error: Not in a Git repository" >&2
    echo "Current directory: $(pwd)" >&2
    exit 1
fi

# Get repository root
REPO_ROOT=$(git rev-parse --show-toplevel)

# Create JSON-RPC request based on operation
case "$OPERATION" in
    status)
        REQUEST=$(cat <<EOF
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "git_status",
    "arguments": {
      "repository": "$REPO_ROOT"
    }
  }
}
EOF
)
        ;;

    log)
        # Parse log options
        MAX_COUNT=""
        AUTHOR=""
        SINCE=""
        UNTIL=""
        GREP=""

        while [[ $# -gt 0 ]]; do
            case $1 in
                --max-count)
                    MAX_COUNT="$2"
                    shift 2
                    ;;
                --author)
                    AUTHOR="$2"
                    shift 2
                    ;;
                --since)
                    SINCE="$2"
                    shift 2
                    ;;
                --until)
                    UNTIL="$2"
                    shift 2
                    ;;
                --grep)
                    GREP="$2"
                    shift 2
                    ;;
                *)
                    shift
                    ;;
            esac
        done

        # Build arguments object
        LOG_ARGS="{\"repository\": \"$REPO_ROOT\""

        if [ -n "$MAX_COUNT" ]; then
            LOG_ARGS="$LOG_ARGS, \"max_count\": $MAX_COUNT"
        fi

        if [ -n "$AUTHOR" ]; then
            LOG_ARGS="$LOG_ARGS, \"author\": \"$AUTHOR\""
        fi

        if [ -n "$SINCE" ]; then
            LOG_ARGS="$LOG_ARGS, \"since\": \"$SINCE\""
        fi

        if [ -n "$UNTIL" ]; then
            LOG_ARGS="$LOG_ARGS, \"until\": \"$UNTIL\""
        fi

        if [ -n "$GREP" ]; then
            LOG_ARGS="$LOG_ARGS, \"grep\": \"$GREP\""
        fi

        LOG_ARGS="$LOG_ARGS}"

        REQUEST=$(cat <<EOF
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "git_log",
    "arguments": $LOG_ARGS
  }
}
EOF
)
        ;;

    diff)
        if [ -z "$1" ]; then
            echo "Error: diff operation requires at least one revision" >&2
            echo "Usage: mcp-git.sh diff <revision> [revision] [path]" >&2
            exit 1
        fi

        REVISION1="$1"
        REVISION2="${2:-}"
        PATH="${3:-}"

        DIFF_ARGS="{\"repository\": \"$REPO_ROOT\", \"revision1\": \"$REVISION1\""

        if [ -n "$REVISION2" ]; then
            DIFF_ARGS="$DIFF_ARGS, \"revision2\": \"$REVISION2\""
        fi

        if [ -n "$PATH" ]; then
            DIFF_ARGS="$DIFF_ARGS, \"path\": \"$PATH\""
        fi

        DIFF_ARGS="$DIFF_ARGS}"

        REQUEST=$(cat <<EOF
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "git_diff",
    "arguments": $DIFF_ARGS
  }
}
EOF
)
        ;;

    blame)
        if [ -z "$1" ]; then
            echo "Error: blame operation requires a file path" >&2
            echo "Usage: mcp-git.sh blame <file>" >&2
            exit 1
        fi

        FILE_PATH="$1"

        REQUEST=$(cat <<EOF
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "git_blame",
    "arguments": {
      "repository": "$REPO_ROOT",
      "path": "$FILE_PATH"
    }
  }
}
EOF
)
        ;;

    show)
        if [ -z "$1" ]; then
            echo "Error: show operation requires a revision" >&2
            echo "Usage: mcp-git.sh show <revision>" >&2
            exit 1
        fi

        REVISION="$1"

        REQUEST=$(cat <<EOF
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "git_show",
    "arguments": {
      "repository": "$REPO_ROOT",
      "revision": "$REVISION"
    }
  }
}
EOF
)
        ;;

    revparse)
        if [ -z "$1" ]; then
            echo "Error: revparse operation requires a revision" >&2
            echo "Usage: mcp-git.sh revparse <revision>" >&2
            exit 1
        fi

        REVISION="$1"

        REQUEST=$(cat <<EOF
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "git_revparse",
    "arguments": {
      "repository": "$REPO_ROOT",
      "revision": "$REVISION"
    }
  }
}
EOF
)
        ;;

    *)
        echo "Error: Unknown operation '$OPERATION'" >&2
        echo "Valid operations: status, log, diff, blame, show, revparse" >&2
        exit 1
        ;;
esac

# Start MCP git server and communicate with it
# The official git MCP server is Python-based
# We use uvx to run it (Python package runner)
RESPONSE=$(echo "$REQUEST" | uvx --from 'mcp[cli]' git-mcp-server 2>/dev/null || echo "")

# Fallback: try npx if there's a Node.js version
if [ -z "$RESPONSE" ]; then
    RESPONSE=$(echo "$REQUEST" | npx -y @modelcontextprotocol/server-git 2>/dev/null || echo "")
fi

# Check if response is empty
if [ -z "$RESPONSE" ]; then
    echo "Error: Failed to get response from MCP git server" >&2
    echo "Possible causes:" >&2
    echo "  - Python/Node.js not installed" >&2
    echo "  - MCP git server package not installed" >&2
    echo "  - Try: pip install 'mcp[cli]' or npm install -g @modelcontextprotocol/server-git" >&2
    exit 1
fi

# Parse JSON response
ERROR=$(echo "$RESPONSE" | jq -r '.error.message // .error // ""' 2>/dev/null || echo "")

if [ -n "$ERROR" ]; then
    echo "Error: $ERROR" >&2
    exit 1
fi

# Extract and format the result
RESULT=$(echo "$RESPONSE" | jq -r '.result.content[0].text // .result // empty' 2>/dev/null)

if [ -z "$RESULT" ]; then
    echo "Error: Unable to extract result from response" >&2
    echo "Response: $RESPONSE" >&2
    exit 1
fi

# Output the result
echo "$RESULT"

exit 0

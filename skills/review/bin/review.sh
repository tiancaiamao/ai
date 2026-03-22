#!/bin/bash
# review.sh - Run code review with reviewer persona
# Usage: review.sh [options] <target>

set -e

TIMEOUT="${TIMEOUT:-10m}"
PERSONA="${PERSONA:-$HOME/.ai/skills/review/reviewer.md}"
OUTPUT="/tmp/review-output.txt"

# Expand ~ and resolve absolute path for persona
resolve_path() {
    local p="$1"
    if [[ "$p" == "~"* ]]; then
        p="$HOME${p:1}"
    fi
    echo "$p"
}
PERSONA=$(resolve_path "$PERSONA")

usage() {
    cat << EOF
Usage: review.sh [options] <target>

Options:
    -t, --timeout DURATION  Timeout (default: 10m)
    -o, --output FILE       Output file (default: /tmp/review-output.txt)
    -p, --persona FILE      Persona file (default: reviewer.md)
    -h, --help              Show this help

Targets:
    diff <ref>              Review changes since ref (e.g., HEAD~1)
    pr <number>             Review PR by number
    file <path>...          Review specific file(s)
EOF
}

# Parse args
TARGET=""
TARGET_TYPE=""

while [[ $# -gt 0 ]]; do
    case $1 in
        -t|--timeout) TIMEOUT="$2"; shift 2 ;;
        -o|--output) OUTPUT="$2"; shift 2 ;;
        -p|--persona) PERSONA="$2"; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        diff) TARGET_TYPE="diff"; TARGET="$2"; shift 2 ;;
        pr) TARGET_TYPE="pr"; TARGET="$2"; shift 2 ;;
        file) TARGET_TYPE="file"; TARGET="$2"; shift 2 ;;
        *) echo "Unknown option: $1"; usage; exit 1 ;;
    esac
done

if [ -z "$TARGET" ]; then
    echo "Error: target required"
    usage
    exit 1
fi

# Build review task
case "$TARGET_TYPE" in
    diff)
        TASK="Review the following git diff (from $TARGET to HEAD):
        
$(git diff $TARGET HEAD 2>/dev/null || git diff $TARGET)"
        ;;
    pr)
        TASK="Review PR #$TARGET. Get details with: gh pr view $TARGET"
        ;;
    file)
        TASK="Review the following files:
        
$(cat $TARGET)"
        ;;
esac

# Write task to file for long descriptions
TASK_FILE="/tmp/review-task-$$.txt"
echo "$TASK" > "$TASK_FILE"

# Run reviewer
# Note: start_subagent_tmux.sh expects just the path, adds @ internally
SESSION=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
    "$OUTPUT" \
    "$TIMEOUT" \
    "$PERSONA" \
    "@$TASK_FILE")

SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)

echo "Starting review..."
~/.ai/skills/tmux/bin/tmux_wait.sh "$SESSION_NAME" "$TIMEOUT"

echo ""
echo "=== Review Complete ==="
echo "Output: $OUTPUT"
cat "$OUTPUT"

# Cleanup
rm -f "$TASK_FILE"

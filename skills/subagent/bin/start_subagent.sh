#!/bin/bash
# start_subagent.sh - Start a subagent and capture session ID
#
# Usage:
#   start_subagent.sh <output_file> <timeout> <system_prompt_file> <task_description>
#
# Output:
#   Writes session ID to stdout
#   Subagent output goes to <output_file>
#
# Example:
#   SESSION=$(start_subagent.sh /tmp/out.txt 10m @explorer.md "Analyze the codebase")
#   ~/.ai/skills/tmux/bin/tmux_wait.sh "$SESSION_NAME" 600
#   cat /tmp/out.txt

set -e

OUTPUT_FILE="$1"
TIMEOUT="$2"
SYSTEM_PROMPT="$3"
TASK="$4"

if [ -z "$OUTPUT_FILE" ] || [ -z "$TIMEOUT" ] || [ -z "$TASK" ]; then
    echo "Usage: start_subagent.sh <output_file> <timeout> <system_prompt_file|-> <task_description>" >&2
    exit 1
fi

# Clear output file
> "$OUTPUT_FILE"

# Build command
CMD="ai --mode headless --timeout $TIMEOUT"
if [ -n "$SYSTEM_PROMPT" ] && [ "$SYSTEM_PROMPT" != "-" ]; then
    CMD="$CMD --system-prompt @$SYSTEM_PROMPT"
fi
CMD="$CMD \"$TASK\""

# Start in background
eval "$CMD > '$OUTPUT_FILE' 2>&1 &"
PID=$!

# Wait for session ID to appear (with timeout)
SESSION_ID=""
for i in $(seq 1 20); do
    sleep 0.2
    SESSION_ID=$(grep -m1 "Session ID:" "$OUTPUT_FILE" 2>/dev/null | awk '{print $3}' || true)
    if [ -n "$SESSION_ID" ]; then
        break
    fi
done

if [ -z "$SESSION_ID" ]; then
    echo "Error: Failed to capture session ID" >&2
    echo "Output so far:" >&2
    cat "$OUTPUT_FILE" >&2
    exit 1
fi

# Output session ID
echo "$SESSION_ID"
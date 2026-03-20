#!/bin/bash
# start_subagent_tmux.sh - Start subagent in tmux session
#
# Usage:
#   start_subagent_tmux.sh <output_file> <timeout> <system_prompt_file> <task_description>
#
# Output:
#   Prints "SESSION_NAME:SESSION_ID" to stdout
#   Subagent output goes to both tmux buffer and <output_file>
#
# Example:
#   RESULT=$(start_subagent_tmux.sh /tmp/out.txt 10m @explorer.md "Analyze code")
#   SESSION_NAME=$(echo $RESULT | cut -d: -f1)
#   SESSION_ID=$(echo $RESULT | cut -d: -f2)
#   ~/.ai/skills/tmux/bin/tmux_wait.sh "$SESSION_NAME" 600

set -e

OUTPUT_FILE="$1"
TIMEOUT="$2"
SYSTEM_PROMPT="$3"
TASK="$4"

if [ -z "$OUTPUT_FILE" ] || [ -z "$TIMEOUT" ] || [ -z "$TASK" ]; then
    echo "Usage: start_subagent_tmux.sh <output_file> <timeout> <system_prompt_file|-> <task_description>" >&2
    exit 1
fi

# Generate unique session name (use random to avoid collisions on macOS where %N is not supported)
SESSION_NAME="subagent-$(date +%s)-$RANDOM$$"

# Clear output file
> "$OUTPUT_FILE"

# Build command arguments array for safe quoting
declare -a CMD_ARGS
CMD_ARGS=(ai --mode headless --timeout "$TIMEOUT")
if [ -n "$SYSTEM_PROMPT" ] && [ "$SYSTEM_PROMPT" != "-" ]; then
    CMD_ARGS+=(--system-prompt "@$SYSTEM_PROMPT")
fi
CMD_ARGS+=("$TASK")

# Build full command with output redirection
FULL_CMD="$(printf '%q ' "${CMD_ARGS[@]}") 2>&1 | tee '$OUTPUT_FILE'"

# Start in tmux session
tmux new -s "$SESSION_NAME" -d
tmux send-keys -t "$SESSION_NAME" -l "$FULL_CMD"
tmux send-keys -t "$SESSION_NAME" C-m

# Wait for session to start and output to appear
sleep 2

# Try to capture Session ID from tmux output
SESSION_ID=""
for i in $(seq 1 30); do
    sleep 0.3
    # Capture entire scrollback buffer, not just visible portion
    OUTPUT=$(tmux capture-pane -t "$SESSION_NAME" -p -S - 2>/dev/null || true)
    SESSION_ID=$(echo "$OUTPUT" | grep -m1 "Session ID:" | awk '{print $3}' || true)

    if [ -n "$SESSION_ID" ]; then
        # Verify session ID format (allow UUID with hyphens)
        if echo "$SESSION_ID" | grep -qE '^[a-zA-Z0-9-]+$'; then
            break
        fi
    fi

    # Check if session still exists
    if ! tmux ls 2>/dev/null | grep -q "^${SESSION_NAME}:"; then
        echo "Error: Tmux session '$SESSION_NAME' ended unexpectedly" >&2
        echo "Output:" >&2
        cat "$OUTPUT_FILE" >&2
        exit 1
    fi
done

if [ -z "$SESSION_ID" ]; then
    echo "Error: Failed to capture Session ID from tmux session" >&2
    echo "Tmux session name: $SESSION_NAME" >&2
    echo "Output so far:" >&2
    cat "$OUTPUT_FILE" >&2
    echo -e "\nTmux capture:" >&2
    tmux capture-pane -t "$SESSION_NAME" -p >&2 || true
    tmux kill-session -t "$SESSION_NAME" 2>/dev/null || true
    exit 1
fi

# Output session name and session ID (colon-separated)
echo "${SESSION_NAME}:${SESSION_ID}"
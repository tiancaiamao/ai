#!/bin/bash
# start_subagent_tmux.sh - Start subagent in tmux session
#
# Usage:
#   start_subagent_tmux.sh [-w] <output_file> <timeout> <system_prompt_file|-> <task_description>
#
# Options:
#   -w: Wait for subagent to complete (uses tmux_wait.sh internally)
#
# Output:
#   Prints "SESSION_NAME:SESSION_ID" to stdout
#   Subagent output goes to both tmux buffer and <output_file>
#
# Example:
#   RESULT=$(start_subagent_tmux.sh /tmp/out.txt 10m @explorer.md "Analyze code")
#   SESSION_NAME=$(echo $RESULT | cut -d: -f1)
#   SESSION_ID=$(echo $RESULT | cut -d: -f2)
#
# Example with -w (wait for completion):
#   start_subagent_tmux.sh -w /tmp/out.txt 10m @explorer.md "Analyze code"

set -e

WAIT_FOR_COMPLETE=false

# Parse -w flag
while [[ "$1" == -* ]]; do
    case "$1" in
        -w|--wait)
            WAIT_FOR_COMPLETE=true
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [-w] <output_file> <timeout> <system_prompt_file|-> <task_description>"
            echo "  -w, --wait: Wait for subagent to complete"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

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
    # Add @ prefix if not already present
    if [ "${SYSTEM_PROMPT:0:1}" = "@" ]; then
        CMD_ARGS+=(--system-prompt "$SYSTEM_PROMPT")
    else
        CMD_ARGS+=(--system-prompt "@$SYSTEM_PROMPT")
    fi
fi
CMD_ARGS+=("$TASK")

# Use a script file for the command to handle exit codes properly
# CMD_SCRIPT takes: <output_file> <cmd> [args...]
CMD_SCRIPT=$(mktemp "/tmp/subagent-cmd-XXXXXX.sh")
cat > "$CMD_SCRIPT" << 'CMDSCRIPT'
set -o pipefail
_output_file="$1"
shift
"$@" 2>&1 | tee "$_output_file"
_exit_code=$?
if [ $_exit_code -eq 0 ]; then
    # Create done marker ONLY on successful completion
    touch "${_output_file}.done"
    # Output explicit done marker to pane for tmux_wait.sh detection
    echo "=== DONE ===" >&2
fi
exit $_exit_code
CMDSCRIPT
chmod +x "$CMD_SCRIPT"

# Build the full command line with proper quoting for tmux
FULL_CMD="$CMD_SCRIPT $OUTPUT_FILE $(printf '%q ' "${CMD_ARGS[@]}")"

# Start in tmux session with the command
tmux new-session -d -s "$SESSION_NAME"
# Send command with Enter
tmux send-keys -t "$SESSION_NAME" "$FULL_CMD" C-m

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
    tmux capture-pane -t "$SESSION_NAME" -p -S - >&2 || true
    # Clean up the session we created
    tmux kill-session -t "$SESSION_NAME" 2>/dev/null || true
    exit 1
fi

# Output session name and session ID (colon-separated)
echo "${SESSION_NAME}:${SESSION_ID}"

# If -w flag was passed, wait for completion
if [ "$WAIT_FOR_COMPLETE" = true ]; then
    echo ""
    echo "Waiting for completion..."
    # Convert timeout to seconds (2m -> 120, 2h -> 7200, 2 -> 2)
    case "$TIMEOUT" in
        *m) TIMEOUT_SECS=$((${TIMEOUT%m} * 60)) ;;
        *h) TIMEOUT_SECS=$((${TIMEOUT%h} * 3600)) ;;
        *) TIMEOUT_SECS=$((TIMEOUT)) ;;
    esac
    TMUX_WAIT="$HOME/.ai/skills/tmux/bin/tmux_wait.sh"
    "$TMUX_WAIT" "$SESSION_NAME" "$OUTPUT_FILE" "$TIMEOUT_SECS" 1
    EXIT_CODE=$?
    
    # Clean up temp script
    rm -f "$CMD_SCRIPT"
    
    case $EXIT_CODE in
        0) echo "Subagent completed successfully" ;;
        1) echo "Error: Subagent timed out"; exit 1 ;;
        3) echo "Error: Subagent exited unexpectedly"; exit 1 ;;
        *) echo "Error: tmux_wait.sh exited with code $EXIT_CODE"; exit 1 ;;
    esac
fi

# Clean up temp script if not waiting
rm -f "$CMD_SCRIPT"
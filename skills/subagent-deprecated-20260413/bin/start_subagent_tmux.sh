#!/bin/bash
# start_subagent_tmux.sh - Start subagent in tmux session
#
# Usage:
#   start_subagent_tmux.sh [-w] [--cleanup MODE] <output_file> <timeout> <system_prompt_file|-> <task_description>
#
# Options:
#   -w: Wait for subagent to complete (uses tmux_wait.sh internally)
#   --cleanup MODE: Cleanup policy for tmux session (always|on-failure|never)
#                   Default: always when -w, never otherwise
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

set -euo pipefail

WAIT_FOR_COMPLETE=false
CLEANUP_MODE=""
CMD_SCRIPT=""
SESSION_NAME=""

cleanup_temp_script() {
    if [ -n "${CMD_SCRIPT:-}" ]; then
        rm -f "$CMD_SCRIPT" 2>/dev/null || true
    fi
}

session_exists() {
    if [ -z "${SESSION_NAME:-}" ]; then
        return 1
    fi
    tmux has-session -t "$SESSION_NAME" 2>/dev/null
}

cleanup_tmux_session() {
    if session_exists; then
        tmux kill-session -t "$SESSION_NAME" 2>/dev/null || true
    fi
}

trap cleanup_temp_script EXIT

# Parse options
while [[ "${1:-}" == -* ]]; do
    case "$1" in
        -w|--wait)
            WAIT_FOR_COMPLETE=true
            shift
            ;;
        --cleanup)
            if [ -z "${2:-}" ]; then
                echo "Error: --cleanup requires a mode (always|on-failure|never)" >&2
                exit 1
            fi
            CLEANUP_MODE="$2"
            shift 2
            ;;
        -h|--help)
            echo "Usage: $0 [-w] [--cleanup MODE] <output_file> <timeout> <system_prompt_file|-> <task_description>"
            echo "  -w, --wait: Wait for subagent to complete"
            echo "  --cleanup MODE: always|on-failure|never"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

OUTPUT_FILE="${1:-}"
TIMEOUT="${2:-}"
SYSTEM_PROMPT="${3:-}"
TASK="${4:-}"

if [ -z "$OUTPUT_FILE" ] || [ -z "$TIMEOUT" ] || [ -z "$TASK" ]; then
    echo "Usage: start_subagent_tmux.sh <output_file> <timeout> <system_prompt_file|-> <task_description>" >&2
    exit 1
fi

if [ -z "$CLEANUP_MODE" ]; then
    if [ "$WAIT_FOR_COMPLETE" = true ]; then
        CLEANUP_MODE="always"
    else
        CLEANUP_MODE="never"
    fi
fi

case "$CLEANUP_MODE" in
    always|on-failure|never) ;;
    *)
        echo "Error: invalid --cleanup mode '$CLEANUP_MODE' (expected always|on-failure|never)" >&2
        exit 1
        ;;
esac

if [ "$WAIT_FOR_COMPLETE" = false ] && [ "$CLEANUP_MODE" != "never" ]; then
    echo "Error: --cleanup=$CLEANUP_MODE requires -w/--wait" >&2
    exit 1
fi

# Generate unique session name (use random to avoid collisions on macOS where %N is not supported)
SESSION_NAME="subagent-$(date +%s)-$RANDOM$$"

# Clear output file
> "$OUTPUT_FILE"

# Build command arguments array for safe quoting
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
    cleanup_tmux_session
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
        *s) TIMEOUT_SECS="${TIMEOUT%s}" ;;
        *m) TIMEOUT_SECS=$((${TIMEOUT%m} * 60)) ;;
        *h) TIMEOUT_SECS=$((${TIMEOUT%h} * 3600)) ;;
        *)
            if [[ "$TIMEOUT" =~ ^[0-9]+$ ]]; then
                TIMEOUT_SECS="$TIMEOUT"
            else
                echo "Error: unsupported timeout format '$TIMEOUT' (use Ns, Nm, Nh, or integer seconds)" >&2
                exit 1
            fi
            ;;
    esac
    TMUX_WAIT="$HOME/.ai/skills/tmux/bin/tmux_wait.sh"
    KILL_ON_FAIL=0
    if [ "$CLEANUP_MODE" != "never" ]; then
        KILL_ON_FAIL=1
    fi
    set +e
    "$TMUX_WAIT" "$SESSION_NAME" "$OUTPUT_FILE" "$TIMEOUT_SECS" 1 "$KILL_ON_FAIL"
    EXIT_CODE=$?
    set -e

    case $EXIT_CODE in
        0)
            if [ "$CLEANUP_MODE" = "always" ]; then
                cleanup_tmux_session
            fi
            echo "Subagent completed successfully"
            ;;
        1)
            if [ "$CLEANUP_MODE" = "on-failure" ] || [ "$CLEANUP_MODE" = "always" ]; then
                cleanup_tmux_session
            fi
            echo "Error: Subagent timed out"
            exit 1
            ;;
        3)
            if [ "$CLEANUP_MODE" = "on-failure" ] || [ "$CLEANUP_MODE" = "always" ]; then
                cleanup_tmux_session
            fi
            echo "Error: Subagent exited unexpectedly"
            exit 1
            ;;
        *) echo "Error: tmux_wait.sh exited with code $EXIT_CODE"; exit 1 ;;
    esac
fi

#!/bin/bash
# tmux_wait.sh - Wait for tmux session to complete
#
# Usage:
#   tmux_wait.sh <session-name> [timeout] [check-interval]
#   OR (recommended for subagents):
#   tmux_wait.sh <session-name> <output-file> [timeout] [check-interval]
#
# If output-file is provided, waits for ${output-file}.done marker instead of
# polling tmux session. This is more reliable and detects completion immediately.
#
# Exit codes:
#   0 - Session completed (ended)
#   1 - Timeout
#   2 - Error (invalid arguments, tmux not available, etc.)

set -e

SESSION_NAME="$1"
shift

# Detect if second argument is a file path or timeout number
if [ $# -gt 0 ] && [[ ! "$1" =~ ^[0-9]+$ ]]; then
    # Second arg is not a number, treat as output file
    OUTPUT_FILE="$1"
    shift
    TIMEOUT="${1:-600}"
    CHECK_INTERVAL="${2:-1}"
else
    # Legacy mode: no output file
    OUTPUT_FILE=""
    TIMEOUT="${1:-3600}"
    CHECK_INTERVAL="${2:-5}"
fi

if [ -z "$SESSION_NAME" ]; then
    echo "Usage: tmux_wait.sh <session-name> [output-file] [timeout] [check-interval]" >&2
    echo "Example: tmux_wait.sh build /tmp/out.txt 600 1" >&2
    exit 2
fi

# Validate tmux is available
if ! command -v tmux &> /dev/null; then
    echo "Error: tmux is not installed or not in PATH" >&2
    exit 2
fi

# Function to get session info
get_session_info() {
    tmux ls 2>/dev/null | grep "^${SESSION_NAME}:" || echo ""
}

# Calculate iterations
ITERATIONS=$((TIMEOUT / CHECK_INTERVAL))

echo "Waiting for tmux session: $SESSION_NAME"
if [ -n "$OUTPUT_FILE" ]; then
    DONE_MARKER="${OUTPUT_FILE}.done"
    echo "Output file: $OUTPUT_FILE"
    echo "Waiting for: $DONE_MARKER"
else
    echo "Polling tmux session (legacy mode)"
fi
echo "Timeout: ${TIMEOUT}s, checking every ${CHECK_INTERVAL}s"
echo ""

# Main polling loop
for i in $(seq 1 $ITERATIONS); do
    if [ -n "$OUTPUT_FILE" ]; then
        # New mode: prefer done marker, fallback to session check
        if [ -f "$DONE_MARKER" ]; then
            echo ""
            echo "✓ Session '${SESSION_NAME}' completed (done marker found)"
            # Clean up marker
            rm -f "$DONE_MARKER"
            exit 0
        fi

        # Fallback: if session no longer exists, consider it complete
        # This handles SIGKILL scenario where trap cannot create done marker
        if ! get_session_info | grep -q .; then
            echo ""
            echo "✓ Session '${SESSION_NAME}' completed (session ended)"
            echo "  Note: Done marker not found (process may have been killed)"
            # Create marker ourselves to prevent confusion
            touch "$DONE_MARKER"
            exit 0
        fi
    else
        # Legacy mode: check if session still exists
        if ! get_session_info | grep -q .; then
            echo ""
            echo "✓ Session '${SESSION_NAME}' completed (no longer exists)"
            exit 0
        fi
    fi

    # Print progress every few iterations
    if [ $CHECK_INTERVAL -ge 10 ]; then
        echo -n "."
    elif [ $((i % (10 / CHECK_INTERVAL))) -eq 0 ]; then
        echo -n "."
    fi

    sleep "$CHECK_INTERVAL"
done

echo ""
echo "✗ Timeout after ${TIMEOUT}s"
if [ -n "$OUTPUT_FILE" ]; then
    echo "Done marker not found: $DONE_MARKER"
else
    echo "Session still running: $(get_session_info)"
fi
echo ""
echo "Last output:"
if [ -n "$OUTPUT_FILE" ] && [ -f "$OUTPUT_FILE" ]; then
    tail -20 "$OUTPUT_FILE"
else
    tmux capture-pane -t "$SESSION_NAME" -p -S -20 2>/dev/null || echo "(no output)"
fi
exit 1
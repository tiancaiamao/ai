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

# Function to check if session is truly idle (no active processes)
is_session_idle() {
    # Check if session contains "completed" in output (our success indicator)
    # Look at more lines to find the completion message
    local pane_content=$(tmux capture-pane -t "$SESSION_NAME" -p -S -10 2>/dev/null)
    if echo "$pane_content" | grep -q "Headless mode completed"; then
        return 0
    fi
    if echo "$pane_content" | grep -q "completed"; then
        return 0
    fi
    return 1
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
            rm -f "$DONE_MARKER"
            exit 0
        fi

        # Check if session contains "completed" text (indicates task finished)
        if is_session_idle; then
            echo ""
            echo "✓ Session '${SESSION_NAME}' completed (output shows completion)"
            touch "$DONE_MARKER"
            exit 0
        fi

        # Fallback: if session no longer exists, consider it complete
        if ! get_session_info | grep -q .; then
            echo ""
            echo "✓ Session '${SESSION_NAME}' completed (session ended)"
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
        # Also check for completion marker in legacy mode
        if is_session_idle; then
            echo ""
            echo "✓ Session '${SESSION_NAME}' completed (output shows completion)"
            exit 0
        fi
    fi

    # Print progress
    echo -n "."
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
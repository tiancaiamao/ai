#!/bin/bash
# tmux_wait.sh - Wait for tmux session to complete via .done marker
#
# Usage:
#   tmux_wait.sh <session-name> <output-file> [timeout] [check-interval]
#
# Arguments:
#   session-name    - Name of tmux session to wait for
#   output-file     - Path to output file (waits for ${output-file}.done)
#   timeout         - Max seconds to wait (default: 600)
#   check-interval  - Seconds between checks (default: 1)
#
# Exit codes:
#   0 - Session completed successfully
#   1 - Timeout
#   2 - Invalid arguments
#   3 - Session ended unexpectedly (no done marker)

set -e

SESSION_NAME="$1"
OUTPUT_FILE="$2"
TIMEOUT="${3:-600}"
CHECK_INTERVAL="${4:-1}"

# Validate arguments
if [ -z "$SESSION_NAME" ] || [ -z "$OUTPUT_FILE" ]; then
    echo "Usage: tmux_wait.sh <session-name> <output-file> [timeout] [check-interval]" >&2
    echo "" >&2
    echo "Example: tmux_wait.sh my-session /tmp/output.txt 600 1" >&2
    echo "" >&2
    echo "This script waits for \${output-file}.done marker to appear." >&2
    echo "Use start_subagent_tmux.sh -w for automatic waiting." >&2
    exit 2
fi

if ! command -v tmux &> /dev/null; then
    echo "Error: tmux is not installed" >&2
    exit 2
fi

DONE_MARKER="${OUTPUT_FILE}.done"
SESSION_END_MARKER="=== DONE ==="

is_session_done() {
    # Check for explicit done marker in pane output
    tmux capture-pane -t "$SESSION_NAME" -p -S -20 2>/dev/null | grep -q "$SESSION_END_MARKER"
}

session_exists() {
    tmux ls 2>/dev/null | grep -q "^${SESSION_NAME}:"
}

echo "Waiting for tmux session: $SESSION_NAME"
echo "Output file: $OUTPUT_FILE"
echo "Waiting for: $DONE_MARKER"
echo "Timeout: ${TIMEOUT}s, checking every ${CHECK_INTERVAL}s"
echo ""

for i in $(seq 1 $((TIMEOUT / CHECK_INTERVAL))); do
    if [ -f "$DONE_MARKER" ]; then
        echo ""
        echo "✓ Session '$SESSION_NAME' completed (done marker)"
        rm -f "$DONE_MARKER"
        exit 0
    fi

    if is_session_done; then
        echo ""
        echo "✓ Session '$SESSION_NAME' completed (output shows completion)"
        touch "$DONE_MARKER"
        exit 0
    fi

    if ! session_exists; then
        # Session 不存在，检查是否是正常结束
        if [ -f "${OUTPUT_FILE}.done" ]; then
            echo ""
            echo "✓ Session '$SESSION_NAME' completed (done marker)"
            rm -f "${OUTPUT_FILE}.done"
            exit 0
        else
            # Session 异常退出（没有 done marker）
            echo ""
            echo "✗ Session '$SESSION_NAME' ended unexpectedly (no done marker)"
            echo ""
            echo "Last output:"
            tail -20 "$OUTPUT_FILE" 2>/dev/null || echo "(no output)"
            exit 3
        fi
    fi

    echo -n "."
    sleep "$CHECK_INTERVAL"
done

echo ""
echo "✗ Timeout after ${TIMEOUT}s"
echo "Done marker not found: $DONE_MARKER"
echo ""
echo "Last output:"
tail -20 "$OUTPUT_FILE" 2>/dev/null || echo "(no output)"
exit 1

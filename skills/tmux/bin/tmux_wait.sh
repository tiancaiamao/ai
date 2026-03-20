#!/bin/bash
# tmux_wait.sh - Wait for tmux session to complete
#
# Usage:
#   tmux_wait.sh <session-name> [timeout] [check-interval]
#
# Arguments:
#   session-name   - Name of the tmux session to wait for
#   timeout        - Timeout in seconds (default: 3600)
#   check-interval - Polling interval in seconds (default: 5)
#
# Exit codes:
#   0 - Session completed (ended)
#   1 - Timeout
#   2 - Error (invalid arguments, tmux not available, etc.)
#
# Examples:
#   # Wait for build session, default timeout
#   tmux_wait.sh build
#
#   # Wait for test session with 10-minute timeout
#   tmux_wait.sh test 600
#
#   # Wait with custom check interval
#   tmux_wait.sh deploy 1800 10

set -e

SESSION_NAME="$1"
TIMEOUT="${2:-3600}"
CHECK_INTERVAL="${3:-5}"

if [ -z "$SESSION_NAME" ]; then
    echo "Usage: tmux_wait.sh <session-name> [timeout] [check-interval]" >&2
    echo "Example: tmux_wait.sh build 600 5" >&2
    exit 2
fi

# Validate tmux is available
if ! command -v tmux &> /dev/null; then
    echo "Error: tmux is not installed or not in PATH" >&2
    exit 2
fi

# Check if session exists at start
if ! tmux ls 2>/dev/null | grep -q "^${SESSION_NAME}:"; then
    echo "Warning: Session '${SESSION_NAME}' not found (may have already completed)" >&2
    exit 0
fi

# Function to get session info
get_session_info() {
    tmux ls 2>/dev/null | grep "^${SESSION_NAME}:" || echo ""
}

# Function to capture last N lines of output
get_output_tail() {
    local lines="${1:-10}"
    tmux capture-pane -t "$SESSION_NAME" -p -S -"$lines" 2>/dev/null || echo ""
}

# Calculate iterations
ITERATIONS=$((TIMEOUT / CHECK_INTERVAL))

echo "Waiting for tmux session: $SESSION_NAME"
echo "Timeout: ${TIMEOUT}s, checking every ${CHECK_INTERVAL}s"
echo "Session info: $(get_session_info)"
echo ""

# Create PID file for potential cleanup
wait_pid_file="/tmp/tmux-wait-$$.pid"
echo $$ > "$wait_pid_file"
trap "rm -f $wait_pid_file" EXIT

# Main polling loop
for i in $(seq 1 $ITERATIONS); do
    # Check if session still exists
    if ! get_session_info | grep -q .; then
        echo ""
        echo "✓ Session '${SESSION_NAME}' completed (no longer exists)"
        exit 0
    fi

    # Print progress every few iterations
    if [ $CHECK_INTERVAL -ge 10 ]; then
        # For longer intervals, print every check
        echo -n "."
    elif [ $((i % (10 / CHECK_INTERVAL))) -eq 0 ]; then
        # For short intervals, print every ~10 seconds
        echo -n "."
    fi

    sleep "$CHECK_INTERVAL"
done

echo ""
echo "✗ Timeout after ${TIMEOUT}s"
echo "Session still running: $(get_session_info)"
echo ""
echo "Last output:"
get_output_tail 20
exit 1

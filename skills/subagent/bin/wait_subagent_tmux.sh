#!/bin/bash
# wait_subagent_tmux.sh - Wait for subagent in tmux session with interrupt support
#
# Usage:
#   wait_subagent_tmux.sh <session_name> [timeout] [interrupt_file]
#
# Exit codes:
#   0 - Completed or interrupted
#   1 - Timeout
#   2 - Error
#
# Example:
#   wait_subagent_tmux.sh subagent-1234567890 600 /tmp/interrupt-file

set -e

SESSION_NAME="$1"
TIMEOUT="${2:-3600}"
INTERRUPT_FILE="${3:-/tmp/ai-interrupt}"

if [ -z "$SESSION_NAME" ]; then
    echo "Usage: wait_subagent_tmux.sh <session_name> [timeout] [interrupt_file]" >&2
    exit 2
fi

# Check if session exists at start
if ! tmux ls 2>/dev/null | grep -q "^${SESSION_NAME}:"; then
    echo "Warning: Session '${SESSION_NAME}' not found (may have already completed)" >&2
    exit 0
fi

# Function to get session output
get_session_output() {
    tmux capture-pane -t "$SESSION_NAME" -p 2>/dev/null || echo ""
}

# Function to send interrupt (Ctrl+C)
send_interrupt() {
    tmux send-keys -t "$SESSION_NAME" C-c
    sleep 1
    # If still running, force kill
    if tmux ls 2>/dev/null | grep -q "^${SESSION_NAME}:"; then
        tmux kill-session -t "$SESSION_NAME" 2>/dev/null || true
    fi
}

# Calculate iterations
CHECK_INTERVAL=5
ITERATIONS=$((TIMEOUT / CHECK_INTERVAL))

echo "Waiting for tmux session: $SESSION_NAME"
echo "Timeout: ${TIMEOUT}s, checking every ${CHECK_INTERVAL}s"
if [ -n "$INTERRUPT_FILE" ]; then
    echo "Interrupt file: $INTERRUPT_FILE"
fi
echo ""

# Create PID file for potential cleanup
wait_pid_file="/tmp/subagent-tmux-wait-$$.pid"
echo $$ > "$wait_pid_file"
trap "rm -f $wait_pid_file" EXIT

# Main polling loop
for i in $(seq 1 $ITERATIONS); do
    # Check for interrupt file
    if [ -n "$INTERRUPT_FILE" ] && [ -f "$INTERRUPT_FILE" ]; then
        echo ""
        echo "Interrupt signal received, sending Ctrl+C to session..."
        send_interrupt
        echo "Session interrupted"
        rm -f "$INTERRUPT_FILE" 2>/dev/null || true
        exit 0
    fi

    # Check if session still exists
    if ! tmux ls 2>/dev/null | grep -q "^${SESSION_NAME}:"; then
        echo ""
        echo "✓ Session '${SESSION_NAME}' completed (no longer exists)"
        exit 0
    fi

    # Print progress every few iterations
    if [ $CHECK_INTERVAL -ge 5 ]; then
        echo -n "."
    elif [ $((i % 2)) -eq 0 ]; then
        echo -n "."
    fi

    sleep "$CHECK_INTERVAL"
done

echo ""
echo "✗ Timeout after ${TIMEOUT}s"
echo "Session still running: $(tmux ls 2>/dev/null | grep "^${SESSION_NAME}:" || echo "Gone")"
echo ""
echo "Last output:"
get_session_output | tail -30

# Offer to kill the session
echo ""
read -p "Kill the timed-out session? [y/N] " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    tmux kill-session -t "$SESSION_NAME" 2>/dev/null
    echo "Session killed"
fi

exit 1

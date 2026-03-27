#!/bin/bash
# wait_any_subagent.sh: Wait for any subagent session to complete
#
# Usage: wait_any_subagent.sh <session1> <session2> ... <sessionN>
# Returns: The name of the first completed session
#
# Example:
#   SESSION1=$(start_subagent_tmux.sh /tmp/out1.txt 10m @p1.md "task1")
#   SESSION2=$(start_subagent_tmux.sh /tmp/out2.txt 10m @p2.md "task2")
#   FIRST=$(wait_any_subagent.sh subagent-1 subagent-2)
#
# Note: This script polls tmux sessions. Session names are like "subagent-123456789"

set -e

MAX_WAIT=600  # 10 minutes
STARTED=$(date +%s)

if [ $# -eq 0 ]; then
    echo "Usage: $0 <session1> <session2> ..." >&2
    exit 1
fi

SESSIONS=("$@")

while true; do
    for session in "${SESSIONS[@]}"; do
        # Check if session no longer exists (completed)
        if ! tmux has-session -t "$session" 2>/dev/null; then
            echo "$session"
            exit 0
        fi
    done

    # Check timeout
    NOW=$(date +%s)
    ELAPSED=$((NOW - STARTED))
    if [ $ELAPSED -gt $MAX_WAIT ]; then
        echo "timeout"
        exit 1
    fi

    sleep 1
done
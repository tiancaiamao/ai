#!/bin/bash
# subagent_wait.sh - Wait for subagent(s) to complete with interrupt support
#
# Usage:
#   subagent_wait.sh <sessions> [timeout] [interrupt_file]
#
# Arguments:
#   sessions      - Comma-separated session IDs (e.g., "abc123,def456")
#   timeout       - Timeout in seconds (default: 600)
#   interrupt_file - File path to watch for user interrupts (optional)
#
# Exit codes:
#   0 - Completed successfully or interrupted
#   1 - Timeout
#   2 - Error

set -e

sessions="$1"
timeout="${2:-600}"
interrupt_file="$3"

if [ -z "$sessions" ]; then
    echo "Usage: subagent_wait.sh <sessions> [timeout] [interrupt_file]" >&2
    exit 2
fi

# Convert comma-separated sessions to array
IFS=',' read -ra SESSION_ARRAY <<< "$sessions"

# Create PID file for cleanup
wait_pid_file="/tmp/ai-wait-$$.pid"
echo $$ > "$wait_pid_file"
trap "rm -f $wait_pid_file" EXIT

# Function to find status file for a session ID
# Sessions are stored in directories like: ~/.ai/sessions/--<cwd>--/<session-id>/
find_status_file() {
    local session_id="$1"
    # Search across all session directories to find the matching session ID
    find "$HOME/.ai/sessions" -path "*/$session_id/status.json" -type f 2>/dev/null | head -1
}

# Function to check if all sessions are completed
check_sessions() {
    all_completed=true
    
    for s in "${SESSION_ARRAY[@]}"; do
        status_file=$(find_status_file "$s")
        
        if [ -z "$status_file" ] || [ ! -f "$status_file" ]; then
            echo "Warning: status file not found for session $s" >&2
            continue
        fi
        
        # Extract status field
        status=$(jq -r .status "$status_file" 2>/dev/null || echo "unknown")
        
        if [ "$status" == "running" ]; then
            # At least one session is still running
            all_completed=false
        fi
    done
    
    # Return 0 if all completed, 1 otherwise
    if [ "$all_completed" == "true" ]; then
        return 0
    else
        return 1
    fi
}

# Function to print status of all sessions
print_status() {
    for s in "${SESSION_ARRAY[@]}"; do
        status_file=$(find_status_file "$s")
        
        if [ -n "$status_file" ] && [ -f "$status_file" ]; then
            status=$(jq -r .status "$status_file" 2>/dev/null || echo "unknown")
            turn=$(jq -r .current_turn "$status_file" 2>/dev/null || echo "?")
            progress=$(jq -r .progress "$status_file" 2>/dev/null || echo "")
            
            echo "Session $s: $status (turn $turn)${progress:+ - $progress}"
        fi
    done
}

# Main loop
echo "Waiting for sessions: ${SESSION_ARRAY[*]}"
echo "Timeout: ${timeout}s"
if [ -n "$interrupt_file" ]; then
    echo "Interrupt file: $interrupt_file"
fi
echo ""

for i in $(seq 1 $((timeout * 2))); do
    # Check for interrupt
    if [ -n "$interrupt_file" ] && [ -f "$interrupt_file" ]; then
        echo ""
        echo "Interrupted by user input"
        rm -f "$interrupt_file"
        exit 0
    fi
    
    # Check session status
    if check_sessions; then
        echo ""
        echo "All sessions completed"
        print_status
        exit 0
    fi
    
    # Print progress every 10 iterations (5 seconds)
    if [ $((i % 10)) -eq 0 ]; then
        echo -n "."
    fi
    
    sleep 0.5
done

echo ""
echo "Timeout after ${timeout}s"
print_status
exit 1
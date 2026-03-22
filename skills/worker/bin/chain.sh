#!/bin/bash
# chain.sh - Execute tasks sequentially, passing output to next task
# Usage: chain.sh [options] <task1> [task2] [task3]...
#   -o, --output-dir DIR   Output directory (default: /tmp)
#   -p, --persona FILE     Persona file for subagent
#   -t, --timeout DURATION Timeout for each task (default: 10m)
#   -k, --keep-going       Continue on error (default: stop on error)

# {previous} in task text will be replaced with previous output

set -e

OUTPUT_DIR="/tmp"
PERSONA=""
TIMEOUT="10m"
KEEP_GOING=false
TASKS=()

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -o|--output-dir)
            OUTPUT_DIR="$2"
            shift 2
            ;;
        -p|--persona)
            PERSONA="$2"
            shift 2
            ;;
        -t|--timeout)
            TIMEOUT="$2"
            shift 2
            ;;
        -k|--keep-going)
            KEEP_GOING=true
            shift
            ;;
        -h|--help)
            echo "Usage: chain.sh [options] <task1> [task2]..."
            echo "  -o, --output-dir DIR   Output directory (default: /tmp)"
            echo "  -p, --persona FILE     Persona file for subagent"
            echo "  -t, --timeout DURATION Timeout (default: 10m)"
            echo "  -k, --keep-going       Continue on error (default: stop)"
            echo ""
            echo "Use {previous} in task text to reference previous output."
            exit 0
            ;;
        *)
            TASKS+=("$1")
            shift
            ;;
    esac
done

if [ ${#TASKS[@]} -eq 0 ]; then
    echo "Error: No tasks provided"
    exit 1
fi

echo "Starting chain of ${#TASKS[@]} tasks"

# Build persona argument
PERSONA_ARG=""
if [ -n "$PERSONA" ]; then
    PERSONA_ARG="@$PERSONA"
fi

PREVIOUS_OUTPUT=""
CHAIN_OUTPUT_FILE="${OUTPUT_DIR}/chain-output.txt"

for i in "${!TASKS[@]}"; do
    task="${TASKS[$i]}"
    step_num=$((i + 1))
    
    # Replace {previous} with actual previous output
    if [[ "$task" == *"{previous}"* ]]; then
        # Escape special characters in previous output for sed
        escaped_prev=$(echo "$PREVIOUS_OUTPUT" | sed 's/[[\.*^$/&]/\\&/g')
        task=$(echo "$task" | sed "s/{previous}/$escaped_prev/g")
    fi
    
    echo ""
    echo "=== Step $step_num/${#TASKS[@]} ==="
    
    # Write task to file if long
    task_file=""
    if [ ${#task} -gt 200 ]; then
        task_file="${OUTPUT_DIR}/chain-task-${i}.txt"
        echo "$task" > "$task_file"
        task="@$task_file"
    fi
    
    output_file="${OUTPUT_DIR}/chain-step-${i}.txt"
    
    # Start subagent
    local session=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
        "$output_file" \
        "$TIMEOUT" \
        "$PERSONA_ARG" \
        "$task" 2>&1)
    
    session_name=$(echo "$session" | cut -d: -f1)
    echo "Running: session=$session_name"
    
    # Wait for completion
    if ~/.ai/skills/tmux/bin/tmux_wait.sh "$session_name" "$TIMEOUT"; then
        echo "✓ Step $step_num completed"
        
        # Capture output as previous for next step
        if [ -f "$output_file" ]; then
            PREVIOUS_OUTPUT=$(cat "$output_file")
            # Also save to combined output
            echo "" >> "$CHAIN_OUTPUT_FILE"
            echo "=== Step $step_num Output ===" >> "$CHAIN_OUTPUT_FILE"
            cat "$output_file" >> "$CHAIN_OUTPUT_FILE"
        fi
    else
        echo "✗ Step $step_num FAILED or TIMEOUT"
        
        if [ -f "$output_file" ]; then
            echo "--- Partial output ---"
            tail -20 "$output_file"
        fi
        
        if [ "$KEEP_GOING" = true ]; then
            echo "Continuing due to --keep-going..."
            PREVIOUS_OUTPUT="[ERROR in step $step_num]"
        else
            echo "Chain stopped at step $step_num"
            exit 1
        fi
    fi
done

echo ""
echo "=== Chain Complete ==="
echo "Full output: $CHAIN_OUTPUT_FILE"
echo "$PREVIOUS_OUTPUT"

exit 0

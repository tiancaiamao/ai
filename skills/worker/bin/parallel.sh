#!/bin/bash
# parallel.sh - Execute tasks in parallel with max concurrency
# Usage: parallel.sh [options] <task1> [task2] [task3]...
#   -n, --max-parallel N   Max parallel tasks (default: 2)
#   -o, --output-dir DIR   Output directory (default: /tmp)
#   -p, --persona FILE     Persona file for subagent
#   -t, --timeout DURATION Timeout for each task (default: 10m)

set -e

MAX_PARALLEL=2
OUTPUT_DIR="/tmp"
PERSONA=""
TIMEOUT="10m"
TASKS_FILE=""  # Optional tasks.md to update
TASKS=()

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -n|--max-parallel)
            MAX_PARALLEL="$2"
            shift 2
            ;;
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
        -f|--tasks-file)
            TASKS_FILE="$2"
            shift 2
            ;;
        -h|--help)
            echo "Usage: parallel.sh [options] <task1> [task2]..."
            echo "  -n, --max-parallel N   Max parallel tasks (default: 2)"
            echo "  -o, --output-dir DIR   Output directory (default: /tmp)"
            echo "  -p, --persona FILE     Persona file for subagent"
            echo "  -t, --timeout DURATION Timeout (default: 10m)"
            echo "  -f, --tasks-file FILE  Tasks.md to auto-update progress"
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

echo "Starting ${#TASKS[@]} tasks with max parallelism: $MAX_PARALLEL"

# Build persona argument
PERSONA_ARG=""
if [ -n "$PERSONA" ]; then
    PERSONA_ARG="@$PERSONA"
fi

# Track PIDs and session names
declare -a PIDS
declare -a SESSIONS
declare -a OUTPUT_FILES

# Function to run a single task
run_task() {
    local index=$1
    local task=$2
    local output_file="${OUTPUT_DIR}/parallel-task-${index}.txt"
    
    # Write task to file if long
    local task_file=""
    if [ ${#task} -gt 200 ]; then
        task_file="${OUTPUT_DIR}/parallel-task-${index}.txt"
        echo "$task" > "$task_file"
        task="@$task_file"
    fi
    
    # Start subagent
    local session=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
        "$output_file" \
        "$TIMEOUT" \
        "$PERSONA_ARG" \
        "$task" 2>&1)
    
    echo "$session"
}

# Start tasks in batches
INDEX=0
TOTAL=${#TASKS[@]}

for task in "${TASKS[@]}"; do
    # Wait if at max parallelism
    while [ $(echo "${PIDS[@]}" | wc -w) -ge $MAX_PARALLEL ]; do
        # Wait for any child to complete
        for i in "${!PIDS[@]}"; do
            if ! kill -0 "${PIDS[$i]}" 2>/dev/null; then
                unset 'PIDS[i]'
                wait "${PIDS[$i]}" 2>/dev/null || true
            fi
        done
        # Compact PIDS array
        PIDS=("${PIDS[@]}")
        sleep 1
    done
    
    # Start this task in background
    (
        output_file="${OUTPUT_DIR}/parallel-task-${INDEX}.txt"
        session=$(run_task $INDEX "$task")
        session_name=$(echo "$session" | cut -d: -f1)
        echo "$session_name" > "${OUTPUT_DIR}/parallel-session-${INDEX}.txt"
        echo "$output_file" > "${OUTPUT_DIR}/parallel-output-${INDEX}.txt"
    ) &
    PIDS+=($!)
    SESSIONS+=("task-${INDEX}")
    OUTPUT_FILES+=("${OUTPUT_DIR}/parallel-task-${INDEX}.txt")
    
    echo "Started: task-${INDEX} (${#PIDS[@]}/${MAX_PARALLEL})"
    INDEX=$((INDEX + 1))
done

# Wait for all remaining tasks
echo "Waiting for all tasks to complete..."
for pid in "${PIDS[@]}"; do
    wait $pid 2>/dev/null || true
done

# Collect results
echo ""
echo "=== Results ==="
SUCCESS=0
FAILED=0

for i in $(seq 0 $((INDEX - 1))); do
    output_file="${OUTPUT_DIR}/parallel-task-${i}.txt"
    session_file="${OUTPUT_DIR}/parallel-session-${i}.txt"
    
    if [ -f "$session_file" ]; then
        session_name=$(cat "$session_file")
        # Check if session completed successfully (wait with 0 timeout)
        if ~/.ai/skills/tmux/bin/tmux_wait.sh "$session_name" 1 2>/dev/null; then
            echo "✓ task-${i}: SUCCESS"
            SUCCESS=$((SUCCESS + 1))
            
            # Auto-update tasks.md if provided
            if [ -n "$TASKS_FILE" ] && [ -f "$TASKS_FILE" ]; then
                task_id=$(echo "${TASKS[$i]}" | head -1 | cut -c1-50 | sed 's/[[\.*^$/&]/\\&/g')
                ~/.ai/skills/worker/bin/update_tasks.sh "$TASKS_FILE" "$task_id" done
            fi
        else
            echo "✗ task-${i}: FAILED or TIMEOUT"
            FAILED=$((FAILED + 1))
            
            # Mark as failed in tasks.md
            if [ -n "$TASKS_FILE" ] && [ -f "$TASKS_FILE" ]; then
                task_id=$(echo "${TASKS[$i]}" | head -1 | cut -c1-50 | sed 's/[[\.*^$/&]/\\&/g')
                ~/.ai/skills/worker/bin/update_tasks.sh "$TASKS_FILE" "$task_id" failed
            fi
        fi
    else
        echo "✗ task-${i}: NO SESSION FILE"
        FAILED=$((FAILED + 1))
    fi
done

echo ""
echo "Summary: $SUCCESS succeeded, $FAILED failed"

# Exit with error if any failed
if [ $FAILED -gt 0 ]; then
    exit 1
fi
exit 0

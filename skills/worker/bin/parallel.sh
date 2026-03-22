#!/bin/bash
# parallel.sh - Execute tasks in parallel with max concurrency
# Usage: parallel.sh [options] <task1> [task2] [task3]...
#   -n, --max-parallel N   Max parallel tasks (default: 2)
#   -o, --output-dir DIR   Output directory (default: /tmp)
#   -p, --persona FILE     Persona file for subagent
#   -t, --timeout DURATION Timeout for each task (default: 10m)
#   -f, --tasks-file FILE  Tasks.md to auto-update progress

set -e

MAX_PARALLEL=2
OUTPUT_DIR="/tmp"
PERSONA=""
TIMEOUT="10m"
TASKS_FILE=""
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
            echo "Usage: parallel.sh [options] <task1> [task2>..."
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

mkdir -p "$OUTPUT_DIR"

echo "Starting ${#TASKS[@]} tasks with max parallelism: $MAX_PARALLEL"

# Build persona argument
PERSONA_ARG=""
if [ -n "$PERSONA" ]; then
    PERSONA_ARG="@$PERSONA"
fi

# Generate unique run ID
RUN_ID="run-$(date +%s)-$$"
SESSIONS_FILE="${OUTPUT_DIR}/${RUN_ID}.sessions"
OUTPUTS_FILE="${OUTPUT_DIR}/${RUN_ID}.outputs"
> "$SESSIONS_FILE"
> "$OUTPUTS_FILE"

# Start all tasks
INDEX=0
for task in "${TASKS[@]}"; do
    output_file="${OUTPUT_DIR}/parallel-task-${RUN_ID}-${INDEX}.txt"
    session_file="${OUTPUT_DIR}/parallel-session-${RUN_ID}-${INDEX}.txt"
    
    # Build task argument (use file if long)
    task_arg="$task"
    if [ ${#task} -gt 200 ]; then
        task_file="${OUTPUT_DIR}/parallel-task-${RUN_ID}-${INDEX}-task.txt"
        echo "$task" > "$task_file"
        task_arg="@$task_file"
    fi
    
    # Write session and output file paths
    echo "$session_file" >> "$SESSIONS_FILE"
    echo "$output_file" >> "$OUTPUTS_FILE"
    
    # Run subagent in background
    (
        session=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
            "$output_file" \
            "$TIMEOUT" \
            "$PERSONA_ARG" \
            "$task_arg" 2>&1)
        session_name=$(echo "$session" | head -1 | cut -d: -f1)
        echo "$session_name" > "$session_file"
    ) &
    
    echo "Started: task-${INDEX} (running)"
    INDEX=$((INDEX + 1))
done

TOTAL=$INDEX

# Wait for all background jobs
echo "Waiting for all tasks to complete..."
wait

# Give a moment for session files to be written
sleep 1

# Collect results
echo ""
echo "=== Results ==="
SUCCESS=0
FAILED=0

i=0
while read session_file; do
    output_file=$(sed -n "$((i + 1))p" "$OUTPUTS_FILE")
    
    if [ -f "$session_file" ]; then
        session_name=$(cat "$session_file")
        
        # Wait for completion
        if ~/.ai/skills/tmux/bin/tmux_wait.sh "$session_name" "$output_file" 60 2>/dev/null; then
            echo "✓ task-${i}: SUCCESS"
            SUCCESS=$((SUCCESS + 1))
            
            # Auto-update tasks.md
            if [ -n "$TASKS_FILE" ] && [ -f "$TASKS_FILE" ]; then
                task_id=$(echo "${TASKS[$i]}" | head -1 | cut -c1-50 | sed 's/[[\.*^$/&]/\\&/g')
                ~/.ai/skills/worker/bin/update_tasks.sh "$TASKS_FILE" "$task_id" done 2>/dev/null || true
            fi
        else
            echo "✗ task-${i}: FAILED or TIMEOUT"
            FAILED=$((FAILED + 1))
            
            if [ -n "$TASKS_FILE" ] && [ -f "$TASKS_FILE" ]; then
                task_id=$(echo "${TASKS[$i]}" | head -1 | cut -c1-50 | sed 's/[[\.*^$/&]/\\&/g')
                ~/.ai/skills/worker/bin/update_tasks.sh "$TASKS_FILE" "$task_id" failed 2>/dev/null || true
            fi
        fi
    else
        echo "✗ task-${i}: NO SESSION FILE ($session_file)"
        FAILED=$((FAILED + 1))
    fi
    i=$((i + 1))
done < "$SESSIONS_FILE"

# Cleanup
rm -f "$SESSIONS_FILE" "$OUTPUTS_FILE"

echo ""
echo "Summary: $SUCCESS succeeded, $FAILED failed"

if [ $FAILED -gt 0 ]; then
    exit 1
fi
exit 0
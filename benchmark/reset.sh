#!/bin/bash
# Reset all tasks to initial state

BENCHMARK_DIR="$(cd "$(dirname "$0")" && pwd)"
TASKS_DIR="$BENCHMARK_DIR/tasks"

reset_task() {
    local task_id=$1
    local task_dir="$TASKS_DIR/$task_id"
    local init_dir="$task_dir/init"
    local setup_dir="$task_dir/setup"

    if [ -d "$init_dir" ]; then
        # Copy init files to setup
        cp -r "$init_dir"/* "$setup_dir"/ 2>/dev/null
        echo "✓ Reset $task_id"
    else
        echo "⚠ No init directory for $task_id"
    fi
}

if [ -n "$1" ]; then
    # Reset specific task
    reset_task "$1"
else
    # Reset all tasks
    echo "Resetting all tasks to initial state..."
    echo ""

    for task_dir in "$TASKS_DIR"/*/; do
        task_id=$(basename "$task_dir")
        if [ -d "$task_dir/init" ]; then
            reset_task "$task_id"
        fi
    done

    echo ""
    echo "Done! All tasks reset to initial state."
fi

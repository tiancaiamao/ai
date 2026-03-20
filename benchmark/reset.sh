#!/bin/bash
# Reset benchmark tasks to a clean state.
# - Restores tracked files under tasks/ via git (if available)
# - Removes untracked generated files under tasks/ via git clean
# - Re-applies init/ -> setup/ overlay when init exists
# - Optional hard mode uses stronger clean to remove nested git repos in tasks

BENCHMARK_DIR="$(cd "$(dirname "$0")" && pwd)"
TASKS_DIR="$BENCHMARK_DIR/tasks"
HARD_MODE=0

if [ "$1" = "--hard" ]; then
    HARD_MODE=1
    shift
fi

is_git_repo() {
    git -C "$BENCHMARK_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1
}

git_restore_path() {
    local rel_path="$1"
    if is_git_repo; then
        git -C "$BENCHMARK_DIR" restore --worktree --staged -- "$rel_path" >/dev/null 2>&1 || true
        if [ "$HARD_MODE" -eq 1 ]; then
            # -ffd: also remove nested git repos inside tasks if any.
            git -C "$BENCHMARK_DIR" clean -fffd -- "$rel_path" >/dev/null 2>&1 || true
        else
            git -C "$BENCHMARK_DIR" clean -fd -- "$rel_path" >/dev/null 2>&1 || true
        fi
    fi
}

reset_task() {
    local task_id=$1
    local task_dir="$TASKS_DIR/$task_id"
    local init_dir="$task_dir/init"
    local setup_dir="$task_dir/setup"

    if [ ! -d "$setup_dir" ]; then
        return 0
    fi

    # Restore tracked files and remove untracked files for this task.
    git_restore_path "tasks/$task_id"

    if [ -d "$init_dir" ] && [ -d "$setup_dir" ]; then
        # Overlay init files to ensure task starts from canonical initial state.
        cp -a "$init_dir"/. "$setup_dir"/ 2>/dev/null || true
        echo "✓ Reset $task_id (git clean + init overlay)"
    elif [ -d "$setup_dir" ]; then
        echo "✓ Reset $task_id (git clean)"
    fi
}

if [ -n "$1" ]; then
    # Reset specific task
    reset_task "$1"
else
    # Reset all tasks
    if [ "$HARD_MODE" -eq 1 ]; then
        echo "Resetting all tasks to clean state (hard mode)..."
    else
        echo "Resetting all tasks to initial state..."
    fi
    echo ""

    for task_dir in "$TASKS_DIR"/*/; do
        task_id=$(basename "$task_dir")
        if [ -d "$task_dir/setup" ]; then
            reset_task "$task_id"
        fi
    done

    echo ""
    if [ "$HARD_MODE" -eq 1 ]; then
        echo "Done! All tasks hard-reset to clean state."
    else
        echo "Done! All tasks reset to clean state."
    fi
fi

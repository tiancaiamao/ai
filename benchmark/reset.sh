#!/bin/bash
# Reset benchmark tasks to a clean state.
# - Restores tracked files under tasks/ via git (if available)
# - Removes untracked generated files under tasks/ via git clean
# - Removes build artifacts and caches (binaries, pycache, etc.)
# - Re-applies init/ -> setup/ overlay when init exists
# - Optional hard mode uses stronger clean to remove nested git repos in tasks

BENCHMARK_DIR="$(cd "$(dirname "$0")" && pwd)"
TASKS_DIR="$BENCHMARK_DIR/tasks"
HARD_MODE=0
KEEP_LATEST_RESULT=1

if [ "$1" = "--hard" ]; then
    HARD_MODE=1
    shift
fi

if [ "$1" = "--no-keep-results" ]; then
    KEEP_LATEST_RESULT=0
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

cleanup_task_artifacts() {
    local task_id=$1
    local task_dir="$TASKS_DIR/$task_id"

    # Remove Python cache (all variations)
    rm -rf "$task_dir/.pytest_cache" 2>/dev/null || true
    rm -rf "$task_dir/setup/__pycache__" 2>/dev/null || true
    rm -rf "$task_dir/tests/__pycache__" 2>/dev/null || true
    find "$task_dir" -type d -name "__pycache__" -exec rm -rf {} + 2>/dev/null || true
    find "$task_dir" -type d -name ".pytest_cache" -exec rm -rf {} + 2>/dev/null || true

    # Remove Python bytecode files
    find "$task_dir" -type f -name "*.pyc" -delete 2>/dev/null || true
    find "$task_dir" -type f -name "*.pyo" -delete 2>/dev/null || true

    # Remove compiled binaries
    find "$task_dir" -type f -name "*.test" -delete 2>/dev/null || true
    find "$task_dir" -type f -perm +111 -name "main" -delete 2>/dev/null || true
    find "$task_dir" -type f -perm +111 -name "assembler" -delete 2>/dev/null || true
    find "$task_dir" -type f -perm +111 -name "counter" -delete 2>/dev/null || true
    find "$task_dir" -type f -perm +111 -name "asm" -delete 2>/dev/null || true
    find "$task_dir" -type f -perm +111 -name "asm_bin" -delete 2>/dev/null || true

    # Remove Go build artifacts
    find "$task_dir" -name "coverage.out" -delete 2>/dev/null || true
    find "$task_dir" -name "coverage.html" -delete 2>/dev/null || true
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

    # Clean up build artifacts and caches
    cleanup_task_artifacts "$task_id"

    if [ -d "$init_dir" ] && [ -d "$setup_dir" ]; then
        # Overlay init files to ensure task starts from canonical initial state.
        cp -a "$init_dir"/. "$setup_dir"/ 2>/dev/null || true
        echo "✓ Reset $task_id (git clean + artifact cleanup + init overlay)"
    elif [ -d "$setup_dir" ]; then
        echo "✓ Reset $task_id (git clean + artifact cleanup)"
    fi
}

cleanup_results() {
    local results_dir="$BENCHMARK_DIR/results"

    # Always remove progress file (it's temporary state)
    rm -f "$results_dir/progress.json"
    rm -rf "$results_dir/.pytest_cache"

    # Remove historical result files, keep latest full result if it exists
    if [ -f "$results_dir/current.json" ]; then
        echo "Preserving: results/current.json (latest full result)"
        find "$results_dir" -name "result_*.json" -delete 2>/dev/null || true
    else
        echo "No results/current.json found, cleaning result files..."
        find "$results_dir" -name "result_*.json" -delete 2>/dev/null || true
    fi

    # If --no-keep-results, also remove current.json and baseline.json
    if [ "$KEEP_LATEST_RESULT" -eq 0 ]; then
        if [ -f "$results_dir/current.json" ]; then
            rm -f "$results_dir/current.json"
            echo "Removed: results/current.json (--no-keep-results)"
        fi
        if [ -f "$results_dir/baseline.json" ]; then
            rm -f "$results_dir/baseline.json"
            echo "Removed: results/baseline.json (--no-keep-results)"
        fi
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

    # Find all tasks with setup/ directory (including nested tasks like tbench/*)
    for task_dir in $(find "$TASKS_DIR" -mindepth 1 -maxdepth 2 -type d); do
        if [ -d "$task_dir/setup" ]; then
            task_id="${task_dir#$TASKS_DIR/}"
            reset_task "$task_id"
        fi
    done

    # Clean up results directory
    echo ""
    echo "Cleaning results directory..."
    cleanup_results

    echo ""
    if [ "$HARD_MODE" -eq 1 ]; then
        echo "Done! All tasks hard-reset to clean state."
    else
        echo "Done! All tasks reset to clean state."
    fi
    if [ "$KEEP_LATEST_RESULT" -eq 1 ]; then
        echo "Latest full result preserved: results/current.json"
    fi
fi

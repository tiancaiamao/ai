#!/bin/bash
# Reset benchmark tasks to a clean state.
# - Restores tracked files under tasks/ via git (if available)
# - Removes untracked generated files under tasks/ via git clean
# - Removes Python cache artifacts (__pycache__, .pytest_cache, *.egg-info)
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

# Remove Python cache artifacts that git clean might miss (ignored files)
clean_python_cache() {
    local task_dir="$1"
    # Remove __pycache__, .pytest_cache, *.egg-info directories
    find "$task_dir" -type d -name "__pycache__" -exec rm -rf {} + 2>/dev/null || true
    find "$task_dir" -type d -name ".pytest_cache" -exec rm -rf {} + 2>/dev/null || true
    find "$task_dir" -type d -name "*.egg-info" -exec rm -rf {} + 2>/dev/null || true
    # Remove .pyc files
    find "$task_dir" -type f -name "*.pyc" -delete 2>/dev/null || true
}

reset_task() {
    local task_id=$1
    local task_dir="$TASKS_DIR/$task_id"
    local init_dir="$task_dir/init"
    local setup_dir="$task_dir/setup"

    # Check if init_dir exists BEFORE git clean (which would remove untracked init/)
    local has_init=0
    if [ -d "$init_dir" ]; then
        has_init=1
    fi

    # Check if setup/ is tracked by git
    local setup_tracked=0
    if is_git_repo && git -C "$BENCHMARK_DIR" ls-files --error-unmatch "tasks/$task_id/setup" >/dev/null 2>&1; then
        setup_tracked=1
    fi

    if [ "$setup_tracked" -eq 1 ]; then
        # setup/ has tracked files — restore and clean
        if is_git_repo; then
            git -C "$BENCHMARK_DIR" restore --worktree --staged -- "tasks/$task_id/setup" >/dev/null 2>&1 || true
            if [ "$HARD_MODE" -eq 1 ]; then
                git -C "$BENCHMARK_DIR" clean -fffd -- "tasks/$task_id/setup" >/dev/null 2>&1 || true
            else
                git -C "$BENCHMARK_DIR" clean -fd -- "tasks/$task_id/setup" >/dev/null 2>&1 || true
            fi
        fi
    elif [ -d "$setup_dir" ]; then
        # setup/ exists but is entirely untracked (e.g., tbench/pypi-server)
        # Remove it — the benchmark runner will re-create it as needed
        rm -rf "$setup_dir"
        echo "✓ Reset $task_id (removed untracked setup/)"
        return 0
    else
        # No setup/ directory exists — nothing to do
        return 0
    fi

    # Clean Python cache artifacts
    clean_python_cache "$task_dir"

    # Re-check init_dir after potential git operations
    if [ "$has_init" -eq 1 ] && [ -d "$init_dir" ] && [ -d "$setup_dir" ]; then
        # First, remove all files from setup/ to ensure clean slate
        # (cp -a only adds, doesn't delete existing files)
        rm -rf "$setup_dir"/*
        rm -rf "$setup_dir"/.[!.]* 2>/dev/null || true
        # Then overlay init files to ensure task starts from canonical initial state.
        cp -a "$init_dir"/. "$setup_dir"/ 2>/dev/null || true
        echo "✓ Reset $task_id (git clean + init overlay)"
    elif [ -d "$setup_dir" ]; then
        echo "✓ Reset $task_id (git clean)"
    fi
}

# Find all task directories with setup/ subdirectory
find_all_task_dirs() {
    # Use find to recursively locate all setup/ directories under tasks/
    # Output the parent directory path relative to tasks/
    find "$TASKS_DIR" -type d -name "setup" | while read -r setup_dir; do
        local task_dir
        task_dir=$(dirname "$setup_dir")
        # Get relative path from tasks/ directory
        local rel_path="${task_dir#$TASKS_DIR/}"
        echo "$rel_path"
    done | sort -u
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

    # Use find to get all task directories (including nested ones like tbench/*)
    while IFS= read -r task_id; do
        [ -z "$task_id" ] && continue
        reset_task "$task_id"
    done < <(find_all_task_dirs)

    echo ""
    if [ "$HARD_MODE" -eq 1 ]; then
        echo "Done! All tasks hard-reset to clean state."
    else
        echo "Done! All tasks reset to clean state."
    fi
fi

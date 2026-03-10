#!/bin/bash
# Run benchmark with your AI agent
# Usage: ./run_agent.sh [task_id]
# Example: ./run_agent.sh 001_fix_off_by_one

set -e

BENCHMARK_DIR="$(cd "$(dirname "$0")" && pwd)"
TASKS_DIR="$BENCHMARK_DIR/tasks"
RUNNER_DIR="$BENCHMARK_DIR/runner"

# Check if ai command is available
if ! command -v ai &> /dev/null; then
    echo "Error: 'ai' command not found. Please install it first:"
    echo "  go install ./cmd/ai"
    exit 1
fi

# Function to run agent on a single task
run_task() {
    local task_id=$1
    local task_dir="$TASKS_DIR/$task_id"
    local setup_dir="$task_dir/setup"

    if [ ! -d "$task_dir" ]; then
        echo "Task not found: $task_id"
        return 1
    fi

    echo "========================================"
    echo "Running task: $task_id"
    echo "========================================"

    # Read task description
    local task_file="$task_dir/task.md"
    if [ ! -f "$task_file" ]; then
        echo "No task.md found for $task_id"
        return 1
    fi

    # Create prompt for the agent
    local prompt="You are given a coding task. Read the task description and fix/implement the code in the setup directory.

Task ID: $task_id
Working Directory: $setup_dir

Task Description:
$(cat "$task_file")

Instructions:
1. Read the files in $setup_dir
2. Fix the bugs or implement the required functionality
3. Make sure the code compiles
4. Do NOT modify any verification scripts (verify.sh)

Please start by reading the task files and understanding what needs to be done."

    # Run the agent in headless mode
    echo "Starting agent..."
    cd "$setup_dir"

    ai --mode headless --max-turns 50 "$prompt" 2>&1 || {
        echo "Agent finished with error for $task_id"
    }

    cd "$BENCHMARK_DIR"
    echo ""
}

# Run verification for a single task
verify_task() {
    local task_id=$1
    local task_dir="$TASKS_DIR/$task_id"
    local verify_script="$task_dir/verify.sh"

    echo "Verifying task: $task_id"

    if [ ! -f "$verify_script" ]; then
        echo "No verify.sh found for $task_id"
        return 1
    fi

    chmod +x "$verify_script"
    bash "$verify_script"
    local result=$?

    if [ $result -eq 0 ]; then
        echo "✅ $task_id PASSED"
    else
        echo "❌ $task_id FAILED"
    fi

    return $result
}

# Main logic
if [ -n "$1" ]; then
    # Run specific task
    run_task "$1"
    verify_task "$1"
else
    # Run all tasks
    echo "Running all benchmark tasks with agent..."
    echo ""

    # Get list of task directories
    tasks=$(ls -d "$TASKS_DIR"/*/ 2>/dev/null | xargs -n1 basename | sort)

    passed=0
    failed=0
    results=""

    for task_dir in $tasks; do
        task_id=${task_dir%/}

        run_task "$task_id"
        if verify_task "$task_id"; then
            passed=$((passed + 1))
            results="$results✅ $task_id\n"
        else
            failed=$((failed + 1))
            results="$results❌ $task_id\n"
        fi
        echo ""
    done

    echo "========================================"
    echo "Summary"
    echo "========================================"
    printf "$results"
    echo ""
    echo "Passed: $passed"
    echo "Failed: $failed"
    echo "Total:  $((passed + failed))"
fi

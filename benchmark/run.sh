#!/bin/bash
# Main benchmark runner script

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

usage() {
    echo "Agent Benchmark Runner"
    echo ""
    echo "Usage: $0 <command>"
    echo ""
    echo "Commands:"
    echo "  run       Run all benchmark tasks"
    echo "  compare   Compare baseline vs current results"
    echo "  list      List all available tasks"
    echo "  baseline  Save current results as baseline"
    echo "  agent     Run with your agent (requires AGENT_CMD env var)"
    echo ""
    echo "Examples:"
    echo "  # Quick test run"
    echo "  $0 run"
    echo ""
    echo "  # Save as baseline"
    echo "  $0 baseline"
    echo ""
    echo "  # Make changes, then compare"
    echo "  $0 run && $0 compare"
    echo ""
    echo "  # Run with your agent"
    echo "  AGENT_CMD='ai --mode rpc' $0 agent"
}

run_benchmark() {
    echo -e "${GREEN}Running benchmark...${NC}"
    go run runner/runner.go run
}

compare_results() {
    echo -e "${YELLOW}Comparing results...${NC}"
    go run runner/runner.go compare
}

list_tasks() {
    go run runner/runner.go list
}

save_baseline() {
    if [ ! -f "results/current.json" ]; then
        echo -e "${RED}Error: No current results found. Run '$0 run' first.${NC}"
        exit 1
    fi
    cp results/current.json results/baseline.json
    echo -e "${GREEN}Baseline saved to results/baseline.json${NC}"
}

run_with_agent() {
    if [ -z "$AGENT_CMD" ]; then
        echo -e "${RED}Error: AGENT_CMD environment variable not set${NC}"
        echo "Example: AGENT_CMD='ai --mode rpc' $0 agent"
        exit 1
    fi

    echo -e "${GREEN}Running benchmark with agent: $AGENT_CMD${NC}"

    # Run each task with the agent
    for task_dir in tasks/*/; do
        task_id=$(basename "$task_dir")
        task_file="${task_dir}task.md"

        if [ ! -f "$task_file" ]; then
            continue
        fi

        echo -e "\n${YELLOW}[$task_id] Running with agent...${NC}"

        # Read task description
        task_desc=$(cat "$task_file")

        # Prepare workspace (copy setup files)
        setup_dir="${task_dir}setup"
        workspace_dir="/tmp/bench_${task_id}"
        rm -rf "$workspace_dir"
        mkdir -p "$workspace_dir"

        if [ -d "$setup_dir" ]; then
            cp -r "$setup_dir"/* "$workspace_dir/"
        fi

        # Create agent prompt
        prompt="You are in directory: $workspace_dir

Please complete the following task:

$task_desc

When done, the verification script will check your work."

        # Run agent
        echo "Prompt: $prompt" > "${task_dir}agent_output.log"
        echo "Workspace: $workspace_dir" >> "${task_dir}agent_output.log"

        # Here you would call your agent
        # Example: echo "$prompt" | $AGENT_CMD >> "${task_dir}agent_output.log"
        echo -e "${YELLOW}Agent placeholder - implement AGENT_CMD integration${NC}"

        # Copy results back
        cp -r "$workspace_dir"/* "${setup_dir}/" 2>/dev/null || true
    done

    # Run verification
    run_benchmark
}

# Main
case "${1:-}" in
    run)
        run_benchmark
        ;;
    compare)
        compare_results
        ;;
    list)
        list_tasks
        ;;
    baseline)
        save_baseline
        ;;
    agent)
        run_with_agent
        ;;
    *)
        usage
        exit 1
        ;;
esac

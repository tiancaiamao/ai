#!/bin/bash
# orchestrate.sh - Orchestrate EDD workflow automatically
# Usage: orchestrate.sh [options] <task-description>
#   -p, --persona DIR    Directory containing personas (default: ~/.ai/skills)
#   -f, --tasks FILE     Tasks.md file (default: tasks.md)
#   -n, --dry-run        Show what would be done without executing
#   -h, --help           Show this help

set -e

WORKFLOW_DIR="${WORKFLOW_DIR:-.workflow}"
STATE_FILE="${WORKFLOW_DIR}/state.json"
PERSONA_DIR="${PERSONA_DIR:-$HOME/.ai/skills}"
TASKS_FILE="tasks.md"
DRY_RUN=false
TASK_DESCRIPTION=""

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -p|--persona-dir)
            PERSONA_DIR="$2"
            shift 2
            ;;
        -f|--tasks)
            TASKS_FILE="$2"
            shift 2
            ;;
        -n|--dry-run)
            DRY_RUN=true
            shift
            ;;
        -h|--help)
            cat << 'EOF'
orchestrate.sh - Orchestrate EDD workflow automatically

Usage:
  orchestrate.sh "Build a REST API"
  orchestrate.sh -f tasks.md -n "Continue work"
  orchestrate.sh status
  orchestrate.sh next

Commands:
  (default)    Run full orchestration loop
  status       Show current progress
  next         Execute next pending task
  init         Initialize workflow

Options:
  -p, --persona-dir DIR   Where to find worker.md, reviewer.md
  -f, --tasks FILE        Path to tasks.md (default: tasks.md)
  -n, --dry-run           Show plan without executing
EOF
            exit 0
            ;;
        status|next|init)
            COMMAND="$1"
            shift
            ;;
        *)
            TASK_DESCRIPTION="$1"
            shift
            ;;
    esac
done

# Helper: count pending tasks
count_pending() {
    grep -c "^\- \[ \]" "$TASKS_FILE" 2>/dev/null || echo "0"
}

# Helper: get next pending task ID
get_next_task_id() {
    grep "^\- \[ \]" "$TASKS_FILE" | head -1 | grep -oE '[A-Z]+[0-9]+' | head -1
}

# Helper: get next pending task description
get_next_pending() {
    grep "^\- \[ \]" "$TASKS_FILE" | head -1 | sed 's/^\- \[.\] //' | sed 's/^[A-Z0-9]* //' | head -1
}

# Helper: update task status
update_task() {
    local pattern="$1"
    local status="$2"

    # Use update_tasks.sh for consistent task updates
    ~/.ai/skills/orchestrate/references/bin/update_tasks.sh "$TASKS_FILE" "$pattern" "$status" 2>/dev/null || true
}

# Show status
do_status() {
    echo "=== EDD Workflow Status ==="
    echo ""
    
    if [ -f "$TASKS_FILE" ]; then
        local total=$(grep -c "^\- \[" "$TASKS_FILE" 2>/dev/null || echo "0")
        local done=$(grep -c "^\- \[X\]" "$TASKS_FILE" 2>/dev/null || echo "0")
        local failed=$(grep -c "^\- \[!\]" "$TASKS_FILE" 2>/dev/null || echo "0")
        local in_progress=$(grep -c "^\- \[-\]" "$TASKS_FILE" 2>/dev/null || echo "0")
        local pending=$(grep -c "^\- \[ \]" "$TASKS_FILE" 2>/dev/null || echo "0")
        
        echo "Tasks: $done/$total done, $pending pending, $in_progress in progress, $failed failed"
        echo ""
        echo "Pending tasks:"
        grep "^\- \[ \]" "$TASKS_FILE" | head -5
    else
        echo "No tasks.md found"
    fi
    
    if [ -f "$STATE_FILE" ]; then
        echo ""
        echo "Workflow state:"
        jq -r '"\(.phase) - \(.started_at // "N/A")"' "$STATE_FILE" 2>/dev/null || cat "$STATE_FILE"
    fi
}

# Execute next task
do_next() {
    local task=$(get_next_pending)
    local task_id=$(get_next_task_id)
    
    if [ -z "$task" ]; then
        echo "✓ All tasks completed!"
        return 0
    fi
    
    echo "Next task: $task (ID: ${task_id:-unknown})"
    
    if [ "$DRY_RUN" = true ]; then
        echo "[DRY RUN] Would execute: $task"
        return 0
    fi
    
    # Mark as in_progress
    if [ -n "$task_id" ]; then
        update_task "$task_id" in_progress
    fi
    
    # Execute via worker
    local worker_persona="${PERSONA_DIR}/orchestrate/references/worker.md"
    local output="/tmp/orchestrate-task-${task_id:-unknown}.txt"
    
    echo "Executing with worker..."
    ~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
        -w "$output" \
        10m \
        "@${worker_persona}" \
        "$task"
    
# Check result - wait for output file to exist with content
    sleep 2
    if [ -f "$output" ] && [ -s "$output" ]; then
        # Check for successful completion markers
        if grep -q "completed successfully\|Headless mode completed\|✓" "$output" 2>/dev/null; then
            echo "✓ Task completed: $task_id"
            update_task "$task_id" done
            
            # Run review
            echo "Running review..."
            local reviewer_persona="${PERSONA_DIR}/orchestrate/references/reviewer.md"
            local review_output="/tmp/orchestrate-review-${task_id:-unknown}.txt"
            
            ~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
                -w "$review_output" \
                5m \
                "@${reviewer_persona}" \
                "Review implementation of $task"
            
            return 0
        fi
    fi
    
    echo "✗ Task failed: $task_id"
    update_task "$task_id" failed
    return 1
}

# Run full orchestration loop
do_orchestrate() {
    if [ ! -f "$TASKS_FILE" ]; then
        echo "Error: No tasks.md found. Run speckit first."
        exit 1
    fi
    
    echo "=== Starting Orchestration ==="
    echo "Tasks file: $TASKS_FILE"
    echo ""
    
    mkdir -p "$WORKFLOW_DIR"
    
    # Initialize state
    cat > "$STATE_FILE" << EOF
{
  "phase": "worker",
  "started_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "tasks_file": "$TASKS_FILE"
}
EOF
    
    # Loop until all tasks done
    local iteration=0
    while [ $(count_pending) -gt 0 ]; do
        iteration=$((iteration + 1))
        echo ""
        echo "--- Iteration $iteration ---"
        
        if ! do_next; then
            echo "Task failed, stopping orchestration"
            return 1
        fi
        
        # Safety limit
        if [ $iteration -gt 20 ]; then
            echo "Max iterations reached, stopping"
            return 1
        fi
    done
    
    echo ""
    echo "=== Orchestration Complete ==="
    echo "All tasks done!"
    
    # Update state
    if [ -f "$STATE_FILE" ]; then
        tmp=$(mktemp)
        jq '.phase = "completed"' "$STATE_FILE" > "$tmp" && mv "$tmp" "$STATE_FILE"
    fi
}

# Main
COMMAND="${COMMAND:-orchestrate}"

case "$COMMAND" in
    status)    do_status ;;
    next)      do_next ;;
    init)      
        mkdir -p "$WORKFLOW_DIR"
        echo '{"phase": "init", "started_at": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'"}' > "$STATE_FILE"
        echo "Workflow initialized"
        ;;
    orchestrate|*)
        if [ -n "$TASK_DESCRIPTION" ]; then
            echo "Note: Task description provided but speckit not automated yet."
            echo "Please run speckit first to create tasks.md"
            exit 1
        fi
        do_orchestrate
        ;;
esac
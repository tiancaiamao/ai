#!/bin/bash
# orchestrate.sh - Multi-agent orchestration loop mode
# Usage: orchestrate.sh [options]
#   -p, --persona-dir DIR    Directory containing personas (default: ~/.ai/skills)
#   -f, --tasks FILE         Tasks.md file (default: tasks.md)
#   -n, --dry-run            Show what would be done without executing
#   -h, --help               Show this help

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
orchestrate.sh - Multi-agent orchestration loop mode

Usage:
  orchestrate.sh
  orchestrate.sh -f tasks.md
  orchestrate.sh status
  orchestrate.sh next

Commands:
  (default)    Run full orchestration loop on tasks.md
  status       Show current progress
  next         Execute next pending task
  init         Initialize workflow

Options:
  -p, --persona-dir DIR   Where to find worker.md, task-checker.md
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
    grep -c "^- \[ \]" "$TASKS_FILE" 2>/dev/null || echo "0"
}

# Helper: get next pending task ID
get_next_task_id() {
    grep "^- \[ \]" "$TASKS_FILE" | head -1 | grep -oE '[A-Z]+[0-9]+' | head -1
}

# Helper: get next pending task description
get_next_pending() {
    grep "^- \[ \]" "$TASKS_FILE" | head -1 | sed 's/^- \[.\] //' | sed 's/^[A-Z0-9]* //' | head -1
}

# Helper: update task status
update_task() {
    local pattern="$1"
    local status="$2"

    # Use update_tasks.sh for consistent task updates
    ~/.ai/skills/orchestrate/references/bin/update_tasks.sh "$TASKS_FILE" "$pattern" "$status" 2>/dev/null || true
}

# Helper: extract check status from task-checker output
extract_check_status() {
    local file="$1"
    grep -oP 'TASK_CHECK_RESULT:\s*\K\{[^}]+\}' "$file" 2>/dev/null | \
        jq -r '.status // ""' 2>/dev/null || echo ""
}

# Helper: extract feedback from task-checker output
extract_feedback() {
    local file="$1"
    local json=$(grep -oP 'TASK_CHECK_RESULT:\s*\K\{[^}]+\}' "$file" 2>/dev/null)
    if [ -n "$json" ]; then
        echo "$json" | jq -r '"\(.next_steps // "")"' 2>/dev/null || \
        echo "$json" | jq -r '"Issues:\n\(.blocking_issues | map("- " + .) | join("\n"))"' 2>/dev/null || \
        cat "$file"
    else
        cat "$file"
    fi
}

# Show status
do_status() {
    echo "=== Orchestration Status ==="
    echo ""
    
    if [ -f "$TASKS_FILE" ]; then
        local total=$(grep -c "^\- \[" "$TASKS_FILE" 2>/dev/null || echo "0")
        local done=$(grep -c "^- \[X\]" "$TASKS_FILE" 2>/dev/null || echo "0")
        local failed=$(grep -c "^- \[!\]" "$TASKS_FILE" 2>/dev/null || echo "0")
        local in_progress=$(grep -c "^- \[-\]" "$TASKS_FILE" 2>/dev/null || echo "0")
        local pending=$(grep -c "^- \[ \]" "$TASKS_FILE" 2>/dev/null || echo "0")
        
        echo "Tasks: $done/$total done, $pending pending, $in_progress in progress, $failed failed"
        echo ""
        echo "Pending tasks:"
        grep "^- \[ \]" "$TASKS_FILE" | head -5
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
    local max_cycles=3
    local cycle=0

    if [ -z "$task" ]; then
        echo "✓ All tasks completed!"
        return 0
    fi

    echo "=== Task: $task (ID: ${task_id:-unknown}) ==="

    if [ "$DRY_RUN" = true ]; then
        echo "[DRY RUN] Would execute: $task"
        return 0
    fi

    # Mark as in_progress
    if [ -n "$task_id" ]; then
        update_task "$task_id" in_progress
    fi

    local worker_persona="${PERSONA_DIR}/orchestrate/references/worker.md"
    local checker_persona="${PERSONA_DIR}/orchestrate/references/task-checker.md"
    local output="/tmp/orchestrate-task-${task_id:-unknown}.txt"
    local check_output="/tmp/orchestrate-check-${task_id:-unknown}.txt"

    # Worker → Task-checker loop
    while [ $cycle -lt $max_cycles ]; do
        cycle=$((cycle + 1))
        echo ""
        echo "--- Cycle $cycle/$max_cycles ---"

        # Build worker prompt
        local worker_prompt="$task"
        if [ $cycle -gt 1 ]; then
            # Get feedback from previous check
            local feedback=$(extract_feedback "$check_output")
            worker_prompt="Fix the following issues for task $task_id:\n\n$feedback"
            echo "Feedback from previous cycle:"
            echo "$feedback" | head -5
            echo ""
        fi

        # Execute worker
        echo "→ Worker executing..."
        ~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
            -w "$output" \
            10m \
            "@${worker_persona}" \
            "$worker_prompt"

        # Check if worker succeeded
        sleep 2
        if [ ! -f "$output" ] || [ ! -s "$output" ]; then
            echo "✗ Worker produced no output"
            update_task "$task_id" failed
            return 1
        fi

        if ! grep -q "completed successfully\|Headless mode completed\|✓" "$output" 2>/dev/null; then
            echo "✗ Worker execution failed"
            update_task "$task_id" failed
            return 1
        fi

        # Run task-checker
        echo "→ Task-checker verifying..."
        ~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
            -w "$check_output" \
            5m \
            "@${checker_persona}" \
            "Check task completion: $task_id"

        # Parse check result
        local status=$(extract_check_status "$check_output")

        case "$status" in
            APPROVED)
                echo "✓ Task approved: $task_id"
                update_task "$task_id" done
                return 0
                ;;
            CHANGES_REQUESTED)
                echo "⚠ Changes requested, cycle $cycle/$max_cycles"
                if [ $cycle -ge $max_cycles ]; then
                    echo "✗ Max cycles reached, manual intervention needed"
                    echo "Latest feedback:"
                    extract_feedback "$check_output" | head -10
                    update_task "$task_id" failed
                    return 1
                fi
                # Continue loop, feedback will be passed to worker
                ;;
            FAILED|"")
                echo "✗ Task failed: $task_id"
                update_task "$task_id" failed
                return 1
                ;;
        esac
    done
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
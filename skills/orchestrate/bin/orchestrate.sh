#!/bin/bash
# orchestrate.sh - Manage EDD workflow state
# Usage: orchestrate.sh [command]

WORKFLOW_DIR="${WORKFLOW_DIR:-.workflow}"
STATE_FILE="${WORKFLOW_DIR}/state.json"

usage() {
    cat << EOF
Usage: orchestrate.sh <command>

Commands:
    init <task>     Initialize new workflow for task
    status          Show current workflow status
    update <phase>  Update current phase (in_progress)
    complete <phase> Mark phase as completed
    abort           Abort workflow
    resume          Resume from abort
EOF
}

# Ensure workflow dir exists
init_workflow_dir() {
    mkdir -p "$WORKFLOW_DIR"
}

# Init new workflow
do_init() {
    local task="$1"
    if [ -z "$task" ]; then
        echo "Error: task required"
        exit 1
    fi
    
    init_workflow_dir
    
    cat > "$STATE_FILE" << EOF
{
  "task": "$task",
  "phase": "init",
  "phases": {
    "explore": {"status": "pending", "started_at": null, "completed_at": null},
    "speckit": {"status": "pending", "started_at": null, "completed_at": null},
    "worker": {"status": "pending", "started_at": null, "completed_at": null},
    "review": {"status": "pending", "started_at": null, "completed_at": null}
  },
  "tasks": {
    "total": 0,
    "done": 0,
    "failed": 0
  },
  "started_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
EOF
    
    echo "Workflow initialized: $task"
}

# Show status
do_status() {
    if [ ! -f "$STATE_FILE" ]; then
        echo "No active workflow. Run 'orchestrate.sh init <task>' to start."
        exit 0
    fi
    
    echo "=== EDD Workflow Status ==="
    jq -r '
    "Task: \(.task)
Phase: \(.phase | ascii_upcase)
Started: \(.started_at // "N/A")

Phases:
  explore: \(.phases.explore.status)
  speckit: \(.phases.speckit.status)
  worker:  \(.phases.worker.status)
  review:  \(.phases.review.status)

Tasks: \(.tasks.done)/\(.tasks.total) done, \(.tasks.failed) failed
"' "$STATE_FILE"
}

# Update phase to in_progress
do_update() {
    local phase="$1"
    if [ -z "$phase" ]; then
        echo "Error: phase required (explore|speckit|worker|review)"
        exit 1
    fi
    
    if [ ! -f "$STATE_FILE" ]; then
        echo "Error: No active workflow"
        exit 1
    fi
    
    # Update phase status using jq
    tmp=$(mktemp)
    jq --arg phase "$phase" '
    .phase = $phase |
    .phases[$phase].status = "in_progress" |
    .phases[$phase].started_at = (now | strftime("%Y-%m-%dT%H:%M:%SZ"))
    ' "$STATE_FILE" > "$tmp" && mv "$tmp" "$STATE_FILE"
    
    echo "Updated phase: $phase (in_progress)"
}

# Complete phase
do_complete() {
    local phase="$1"
    if [ -z "$phase" ]; then
        echo "Error: phase required"
        exit 1
    fi
    
    if [ ! -f "$STATE_FILE" ]; then
        echo "Error: No active workflow"
        exit 1
    fi
    
    tmp=$(mktemp)
    jq --arg phase "$phase" '
    .phases[$phase].status = "completed" |
    .phases[$phase].completed_at = (now | strftime("%Y-%m-%dT%H:%M:%SZ"))
    ' "$STATE_FILE" > "$tmp" && mv "$tmp" "$STATE_FILE"
    
    echo "Phase completed: $phase"
}

# Abort workflow
do_abort() {
    if [ ! -f "$STATE_FILE" ]; then
        echo "No active workflow to abort"
        exit 0
    fi
    
    echo "Aborting workflow..."
    tmp=$(mktemp)
    jq '.phase = "aborted"' "$STATE_FILE" > "$tmp" && mv "$tmp" "$STATE_FILE"
    echo "Workflow aborted. State preserved in $STATE_FILE"
}

# Resume workflow
do_resume() {
    if [ ! -f "$STATE_FILE" ]; then
        echo "No workflow to resume"
        exit 1
    fi
    
    local phase=$(jq -r '.phase' "$STATE_FILE")
    if [ "$phase" = "aborted" ]; then
        tmp=$(mktemp)
        jq '.phase = "init"' "$STATE_FILE" > "$tmp" && mv "$tmp" "$STATE_FILE"
        echo "Workflow resumed"
    else
        echo "Workflow is active (phase: $phase)"
    fi
}

# Main
case "${1:-}" in
    init)      do_init "$2" ;;
    status)    do_status ;;
    update)    do_update "$2" ;;
    complete)  do_complete "$2" ;;
    abort)     do_abort ;;
    resume)    do_resume ;;
    *)         usage ;;
esac

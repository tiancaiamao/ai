#!/bin/bash
# workflow.sh - Execute workflow phases

set -e

WORKFLOW_DIR=".workflow"
STATE_FILE="$WORKFLOW_DIR/STATE.json"
SKILL_DIR="${HOME}/.ai/skills/workflow"

usage() {
    cat <<EOF
Usage: workflow.sh <command>

Commands:
    start <template> [description]  Start a workflow
    auto                               Execute current workflow
    status                             Show workflow state
    templates                          List available templates
    templates info <name>              Show template details
    pause                              Pause auto mode
    resume                             Resume paused workflow
    stop                               Stop workflow
    next                               Execute next phase
    commit                             Commit current phase

Examples:
    workflow.sh start bugfix "login timeout"
    workflow.sh auto
    workflow.sh status
EOF
}

# Load state
load_state() {
    if [[ ! -f "$STATE_FILE" ]]; then
        echo "No workflow state found. Run 'workflow.sh start' first."
        exit 1
    fi
    cat "$STATE_FILE"
}

# Get current phase
get_current_phase() {
    local state=$(load_state)
    echo "$state" | jq -r '.phases[.currentPhase].name // empty'
}

# Get phase index
get_phase_index() {
    local state=$(load_state)
    echo "$state" | jq -r '.currentPhase'
}

# Advance to next phase
advance_phase() {
    local state=$(load_state)
    local next=$(( $(get_phase_index) + 1 ))
    local total=$(echo "$state" | jq '.phases | length')
    
    if [[ $next -ge $total ]]; then
        echo "All phases complete!"
        echo "$state" | jq ".status = 'completed' | .completedAt = '$(date -u +%Y-%m-%dT%H:%M:%SZ)'" > "$STATE_FILE"
        return 0
    fi
    
    # Mark current as completed
    echo "$state" | jq \
        ".phases[$(( $(get_phase_index) ))].status = 'completed' | \
         .currentPhase = $next | \
         .phases[$next].status = 'active' | \
         .updatedAt = '$(date -u +%Y-%m-%dT%H:%M:%SZ)'" \
        > "$STATE_FILE.tmp" && mv "$STATE_FILE.tmp" "$STATE_FILE"
    
    echo "Advanced to phase: $(get_current_phase)"
}

# Commit current phase
commit_phase() {
    local phase=$(get_current_phase)
    local template=$(load_state | jq -r '.template')
    
    git add -A
    git commit -m "feat($template): complete $phase phase"
}

# List templates
list_templates() {
    local registry="$SKILL_DIR/templates/registry.json"
    
    if [[ ! -f "$registry" ]]; then
        echo "Registry not found at $registry"
        exit 1
    fi
    
    echo "Available templates:"
    echo ""
    jq -r '.templates | to_entries[] | "- \(.key): \(.value.name) (\(.value.complexity))"' "$registry"
}

# Show template info
template_info() {
    local name="$1"
    local registry="$SKILL_DIR/templates/registry.json"
    
    if [[ ! -f "$registry" ]]; then
        echo "Registry not found"
        exit 1
    fi
    
    jq ".templates[\"$name\"]" "$registry" 2>/dev/null || echo "Template '$name' not found"
}

# Main commands
cmd_start() {
    local template="${1:-feature}"
    local description="${2:-}"
    local registry="$SKILL_DIR/templates/registry.json"
    
    # Load template
    local template_data=$(jq ".templates[\"$template\"]" "$registry" 2>/dev/null)
    if [[ -z "$template_data" || "$template_data" == "null" ]]; then
        echo "Unknown template: $template"
        echo "Run 'workflow.sh templates' for available templates"
        exit 1
    fi
    
    local name=$(echo "$template_data" | jq -r '.name')
    local phases=$(echo "$template_data" | jq -r '.phases | join(", ")')
    local category=$(echo "$template_data" | jq -r '.category')
    local complexity=$(echo "$template_data" | jq -r '.complexity')
    
    # Create directories
    mkdir -p "$WORKFLOW_DIR/$category"
    
    # Generate artifact directory name
    local date_prefix=$(date +"%y%m%d")
    local slug=$(echo "$description" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9]*-/--/g' | head -c 40)
    local artifact_dir="$WORKFLOW_DIR/$category/${date_prefix}-1-${slug:-unnamed}"
    mkdir -p "$artifact_dir"
    
    # Create state
    cat > "$STATE_FILE" <<EOF
{
  "template": "$template",
  "templateName": "$name",
  "description": "$description",
  "phases": [
    {"name": "$(echo "$template_data" | jq -r '.phases[0]')", "index": 0, "status": "active"}
$(for i in $(seq 1 $(($(echo "$template_data" | jq '.phases | length') - 1))); do
    echo "    ,{\"name\": \"$(echo "$template_data" | jq -r ".phases[$i]")\", \"index\": $i, \"status\": \"pending\"}"
done)
  ],
  "currentPhase": 0,
  "status": "in_progress",
  "startedAt": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "artifactDir": "$artifact_dir"
}
EOF
    
    # Copy template
    cp "$SKILL_DIR/templates/$template.md" "$artifact_dir/TEMPLATE.md"
    
    echo "Workflow started: $name"
    echo "Description: $description"
    echo "Phases: $phases"
    echo "Artifact dir: $artifact_dir"
    echo ""
    echo "Run 'workflow.sh status' to see current state"
}

cmd_status() {
    if [[ ! -f "$STATE_FILE" ]]; then
        echo "No active workflow"
        exit 1
    fi
    
    echo "=== Workflow Status ==="
    cat "$STATE_FILE" | jq '
        "Template: \(.templateName)",
        "Description: \(.description)",
        "Status: \(.status)",
        "Started: \(.startedAt)",
        "",
        "Phases:",
        (.phases | to_entries[] | "  \(.value.index + 1). [\(if .value.status == "active" then "→" else if .value.status == "completed" then "✓" else " " end end)] \(.value.name)")
    '
}

cmd_auto() {
    # Auto mode: auto-detect and advance workflow
    # Similar to GSD-2 /cron wf-tick
    # 
    # Detection logic:
    # 1. Check if artifact dir has phase completion marker
    # 2. Check if git has "complete <phase>" commit
    # 3. If detected, auto-advance
    
    if [[ ! -f "$STATE_FILE" ]]; then
        echo "No active workflow"
        echo ""
        echo "Start one with:"
        echo "  workflow.sh start bugfix \"description\""
        echo "  workflow.sh start feature \"description\""
        echo ""
        echo "Or run 'workflow.sh templates' to see options"
        return 0
    fi
    
    local state=$(load_state)
    local status=$(echo "$state" | jq -r '.status')
    
    # Check if workflow is completed
    if [[ "$status" == "completed" ]]; then
        echo "✅ Workflow completed!"
        echo ""
        echo "Template: $(echo "$state" | jq -r '.templateName')"
        echo "Description: $(echo "$state" | jq -r '.description')"
        echo "Started: $(echo "$state" | jq -r '.startedAt')"
        if [[ -f "$STATE_FILE" ]] && grep -q "completedAt" "$STATE_FILE" 2>/dev/null; then
            echo "Finished: $(echo "$state" | jq -r '.completedAt')"
        fi
        echo ""
        echo "Artifacts saved to: $(echo "$state" | jq -r '.artifactDir')"
        echo ""
        echo "Run 'workflow.sh stop' to clean up, or 'workflow.sh start' for a new workflow"
        return 0
    fi
    
    local phase=$(get_current_phase)
    local phase_idx=$(get_phase_index)
    local template=$(echo "$state" | jq -r '.template')
    local artifact=$(echo "$state" | jq -r '.artifactDir')
    
    # Check for phase completion markers
    local phase_done=false
    local completion_marker="$artifact/phase-$phase_idx-done"
    
    if [[ -f "$completion_marker" ]]; then
        # Phase marker file exists - mark as done
        phase_done=true
        echo "📍 Detected phase completion marker"
    elif git rev-parse --git-dir > /dev/null 2>&1; then
        # Check git log for "complete <phase>" commit
        local commit_msg=$(git log -1 --pretty=%B 2>/dev/null | head -1)
        if [[ "$commit_msg" =~ "complete $phase" ]]; then
            phase_done=true
            echo "📍 Detected git commit: $commit_msg"
        fi
    fi
    
    if [[ "$phase_done" == "true" ]]; then
        echo ""
        echo "✓ Phase '$phase' completed!"
        echo ""
        
        # Mark phase as done in state - rebuild JSON to fix formatting
        local total=$(echo "$state" | jq '.phases | length')
        local next_idx=$((phase_idx + 1))
        
        if [[ $next_idx -ge $total ]]; then
            # All phases done - rebuild state file
            cat > "$STATE_FILE" <<EOF
{
  "template": "$template",
  "templateName": "$(echo "$state" | jq -r '.templateName')",
  "description": "$(echo "$state" | jq -r '.description')",
  "phases": [
    $(for i in $(seq 0 $((total - 1))); do
        local name=$(echo "$state" | jq -r ".phases[$i].name")
        local status="completed"
        if [[ $i -eq $next_idx ]]; then status="active"; elif [[ $i -lt $next_idx ]]; then status="completed"; else status="pending"; fi
        echo "    {\"name\": \"$name\", \"index\": $i, \"status\": \"$status\"}"
        if [[ $i -lt $((total - 1)) ]]; then echo ","; fi
    done)
  ],
  "currentPhase": $next_idx,
  "status": "completed",
  "startedAt": "$(echo "$state" | jq -r '.startedAt')",
  "completedAt": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "artifactDir": "$artifact"
}
EOF
            echo "🎉 All phases completed!"
            echo ""
            echo "Template: $(echo "$state" | jq -r '.templateName')"
            echo "Artifacts: $artifact"
            echo ""
            echo "Run 'workflow.sh stop' to clean up."
        else
            # Rebuild state with next phase active
            cat > "$STATE_FILE" <<EOF
{
  "template": "$template",
  "templateName": "$(echo "$state" | jq -r '.templateName')",
  "description": "$(echo "$state" | jq -r '.description')",
  "phases": [
    $(for i in $(seq 0 $((total - 1))); do
        local name=$(echo "$state" | jq -r ".phases[$i].name")
        local status="pending"
        if [[ $i -eq $next_idx ]]; then status="active"
        elif [[ $i -lt $next_idx ]]; then status="completed"; fi
        echo "    {\"name\": \"$name\", \"index\": $i, \"status\": \"$status\"}"
        if [[ $i -lt $((total - 1)) ]]; then echo ","; fi
    done)
  ],
  "currentPhase": $next_idx,
  "status": "in_progress",
  "startedAt": "$(echo "$state" | jq -r '.startedAt')",
  "updatedAt": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "artifactDir": "$artifact"
}
EOF
            local next_phase=$(get_current_phase)
            echo "→ Advancing to next phase: $next_phase"
            echo ""
            echo "Current state:"
            cmd_status
        fi
    else
        # No completion detected - show status
        echo "=== Workflow In Progress ==="
        echo ""
        echo "Current phase: $phase (phase $((phase_idx + 1)))"
        echo ""
        
        # Show phase instructions
        if [[ -f "$artifact/TEMPLATE.md" ]]; then
            local phase_num=$((phase_idx + 1))
            local phase_content=$(sed -n "/## Phase $phase_num:/,/## Phase/p" "$artifact/TEMPLATE.md" 2>/dev/null | head -25)
            
            if [[ -n "$phase_content" && "$phase_content" != *"## Phase"* ]]; then
                echo "Phase instructions:"
                echo "$phase_content" | head -15
                echo "..."
            fi
        fi
        
        echo ""
        echo "💡 Complete this phase, then run 'workflow.sh auto' again"
        echo "   (or create marker file: touch $completion_marker)"
    fi
}

cmd_next() {
    if [[ ! -f "$STATE_FILE" ]]; then
        echo "No active workflow. Run 'workflow.sh start' first."
        exit 1
    fi
    
    local phase=$(get_current_phase)
    local artifact=$(load_state | jq -r '.artifactDir')
    
    echo "=== Executing Phase: $phase ==="
    echo "Artifact dir: $artifact"
    echo ""
    echo "Instructions:"
    local phase_cap
    phase_cap="$(echo "$phase" | sed 's/.*/\U&/')"
    cat "$artifact/TEMPLATE.md" | grep -A 20 "## Phase [0-9]*: $phase_cap" || true
    echo ""
    echo "After completing the phase:"
    echo "  1. Update artifacts in $artifact/"
    echo "  2. Run 'workflow.sh commit' to commit"
    echo "  3. Run 'workflow.sh next' to advance"
}

cmd_commit() {
    commit_phase
    echo "Phase committed"
}

cmd_pause() {
    if [[ ! -f "$STATE_FILE" ]]; then
        echo "No active workflow."
        exit 1
    fi
    
    local state=$(cat "$STATE_FILE")
    local status=$(echo "$state" | jq -r '.status')
    
    if [[ "$status" == "paused" ]]; then
        echo "Already paused."
        return
    fi
    
    echo "$state" | jq '.status = "paused" | .pausedAt = "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'"' > "$STATE_FILE"
    echo "Workflow paused."
}

cmd_resume() {
    if [[ ! -f "$STATE_FILE" ]]; then
        echo "No active workflow."
        exit 1
    fi
    
    local state=$(cat "$STATE_FILE")
    local status=$(echo "$state" | jq -r '.status')
    
    if [[ "$status" != "paused" ]]; then
        echo "Workflow is not paused (status: $status)."
        return
    fi
    
    echo "$state" | jq '.status = "in_progress" | .resumedAt = "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'"' > "$STATE_FILE"
    local phase=$(get_current_phase)
    echo "Workflow resumed. Current phase: $phase"
    cmd_status
}

cmd_stop() {
    if [[ ! -f "$STATE_FILE" ]]; then
        echo "No active workflow."
        return
    fi
    
    local artifact=$(load_state | jq -r '.artifactDir')
    
    read -p "Stop workflow and remove artifacts in $artifact? [y/N] " confirm
    if [[ "$confirm" =~ ^[Yy]$ ]]; then
        rm -rf "$WORKFLOW_DIR"
        echo "Workflow stopped and artifacts removed."
    else
        echo "Cancelled."
    fi
}

# Parse command
case "${1:-}" in
    start)
        shift
        cmd_start "$@"
        ;;
    auto)
        cmd_auto
        ;;
    status)
        cmd_status
        ;;
    templates)
        shift
        if [[ "${1:-}" == "info" ]]; then
            template_info "${2:-}"
        else
            list_templates
        fi
        ;;
    pause)
        cmd_pause
        ;;
    resume)
        cmd_resume
        ;;
    stop)
        cmd_stop
        ;;
    next)
        cmd_next
        ;;
    commit)
        cmd_commit
        ;;
    -h|--help|help)
        usage
        ;;
    *)
        usage
        exit 1
        ;;
esac
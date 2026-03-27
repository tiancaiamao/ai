#!/bin/bash
# orchestrate.sh - Phase-level orchestration with review loops
# Usage: orchestrate.sh [options]
#   -p, --persona-dir DIR    Directory containing personas (default: ~/.ai/skills)
#   -n, --dry-run            Show what would be done without executing
#   -h, --help               Show this help

set -e

WORKFLOW_DIR="${WORKFLOW_DIR:-.workflow}"
STATE_FILE="${WORKFLOW_DIR}/state.json"
PERSONA_DIR="${PERSONA_DIR:-$HOME/.ai/skills}"
DRY_RUN=false
MAX_CYCLES=3

# Phase definitions
PHASES=("SPECIFY" "PLAN" "TASKS" "IMPLEMENT")
PHASE_OUTPUTS=("spec.md" "plan.md" "tasks.md" "code")

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -p|--persona-dir)
            PERSONA_DIR="$2"
            shift 2
            ;;
        -n|--dry-run)
            DRY_RUN=true
            shift
            ;;
        -h|--help)
            cat << 'EOF'
orchestrate.sh - Phase-level orchestration

Usage:
  orchestrate.sh           Run full workflow
  orchestrate.sh status    Show current progress
  orchestrate.sh next      Execute next phase
  orchestrate.sh init      Initialize workflow

Workflow:
  For each phase (SPECIFY → PLAN → TASKS → IMPLEMENT):
    while not approved:
      1. phase-worker executes entire phase
      2. phase-reviewer reviews output
      3. if approved: commit, move to next phase
      4. if changes needed: loop back with feedback

Options:
  -p, --persona-dir DIR   Where to find phase-worker.md, phase-reviewer.md
  -n, --dry-run           Show plan without executing
EOF
            exit 0
            ;;
        status|next|init)
            COMMAND="$1"
            shift
            ;;
        *)
            shift
            ;;
    esac
done

# Helper: get current phase
get_current_phase() {
    if [ -f "$STATE_FILE" ]; then
        jq -r '.phase // "SPECIFY"' "$STATE_FILE" 2>/dev/null || echo "SPECIFY"
    else
        # Auto-detect based on files
        if [ ! -f "spec.md" ]; then
            echo "SPECIFY"
        elif [ ! -f "plan.md" ]; then
            echo "PLAN"
        elif [ ! -f "tasks.md" ]; then
            echo "TASKS"
        else
            echo "IMPLEMENT"
        fi
    fi
}

# Helper: get phase index
get_phase_index() {
    local phase="$1"
    for i in "${!PHASES[@]}"; do
        if [ "${PHASES[$i]}" = "$phase" ]; then
            echo $i
            return
        fi
    done
    echo 0
}

# Helper: extract review status
extract_review_status() {
    local file="$1"
    grep -oP 'PHASE_REVIEW_RESULT:\s*\K\{[^}]+\}' "$file" 2>/dev/null | \
        jq -r '.status // ""' 2>/dev/null || echo ""
}

# Helper: extract review feedback
extract_feedback() {
    local file="$1"
    local json=$(grep -oP 'PHASE_REVIEW_RESULT:\s*\K\{[^}]+\}' "$file" 2>/dev/null)
    if [ -n "$json" ]; then
        local next_steps=$(echo "$json" | jq -r '.next_steps // ""' 2>/dev/null)
        local issues=$(echo "$json" | jq -r '.blocking_issues | join("\n- ")' 2>/dev/null)
        if [ -n "$next_steps" ]; then
            echo "$next_steps"
        elif [ -n "$issues" ]; then
            echo "Issues to fix:"
            echo "- $issues"
        fi
    else
        cat "$file"
    fi
}

# Helper: commit phase
commit_phase() {
    local phase="$1"
    local phase_lower=$(echo "$phase" | tr '[:upper:]' '[:lower:]')
    
    # Check if there's anything to commit
    if git diff --quiet 2>/dev/null && git diff --cached --quiet 2>/dev/null; then
        echo "No changes to commit"
        return 0
    fi
    
    echo "→ Committing phase: $phase"
    git add -A
    git commit -m "feat($phase_lower): complete $phase_lower phase" || echo "Nothing to commit"
}

# Helper: update state
update_state() {
    local phase="$1"
    local status="$2"
    local cycle="${3:-0}"
    
    mkdir -p "$WORKFLOW_DIR"
    cat > "$STATE_FILE" << EOF
{
  "phase": "$phase",
  "status": "$status",
  "cycle": $cycle,
  "updated_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
EOF
}

# Show status
do_status() {
    echo "=== Orchestration Status ==="
    echo ""
    
    local phase=$(get_current_phase)
    echo "Current Phase: $phase"
    echo ""
    
    echo "Phase Outputs:"
    for p in "${PHASES[@]}"; do
        local output=""
        case $p in
            SPECIFY) output="spec.md" ;;
            PLAN) output="plan.md" ;;
            TASKS) output="tasks.md" ;;
            IMPLEMENT) output="code" ;;
        esac
        
        if [ "$p" = "IMPLEMENT" ]; then
            if [ -f "tasks.md" ]; then
                local done=$(grep -c "^- \[X\]" tasks.md 2>/dev/null || echo "0")
                local total=$(grep -c "^- \[" tasks.md 2>/dev/null || echo "0")
                echo "  [$p] $done/$total tasks"
            else
                echo "  [$p] (waiting)"
            fi
        elif [ -f "$output" ]; then
            echo "  [$p] ✓ $output"
        else
            echo "  [$p] ○ pending"
        fi
    done
    
    if [ -f "$STATE_FILE" ]; then
        echo ""
        echo "State:"
        jq '.' "$STATE_FILE" 2>/dev/null || cat "$STATE_FILE"
    fi
}

# Execute one phase
do_phase() {
    local phase="$1"
    local cycle=0
    
    local worker_persona="${PERSONA_DIR}/orchestrate/references/phase-worker.md"
    local reviewer_persona="${PERSONA_DIR}/orchestrate/references/phase-reviewer.md"
    local output="/tmp/orchestrate-phase-${phase}.txt"
    local review_output="/tmp/orchestrate-review-${phase}.txt"
    
    echo "=== Phase: $phase ==="
    
    if [ "$DRY_RUN" = true ]; then
        echo "[DRY RUN] Would execute phase: $phase"
        return 0
    fi
    
    # Phase review loop
    while [ $cycle -lt $MAX_CYCLES ]; do
        cycle=$((cycle + 1))
        echo ""
        echo "--- Cycle $cycle/$MAX_CYCLES ---"
        
        # Build worker prompt
        local worker_prompt="Execute phase: $phase"
        if [ $cycle -gt 1 ]; then
            local feedback=$(extract_feedback "$review_output")
            worker_prompt="Fix issues in phase $phase:

$feedback

Address ALL issues above, then report completion."
            echo "Feedback from previous review:"
            echo "$feedback" | head -10
            echo ""
        fi
        
        # Execute worker
        echo "→ Phase-worker executing..."
        update_state "$phase" "in_progress" $cycle
        
        ~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
            -w "$output" \
            15m \
            "@${worker_persona}" \
            "$worker_prompt"
        
        # Check if worker succeeded
        sleep 2
        if [ ! -f "$output" ] || [ ! -s "$output" ]; then
            echo "✗ Phase-worker produced no output"
            update_state "$phase" "failed" $cycle
            return 1
        fi
        
        # Run phase-reviewer
        echo "→ Phase-reviewer checking..."
        ~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
            -w "$review_output" \
            5m \
            "@${reviewer_persona}" \
            "Review phase: $phase"
        
        # Parse review result
        local status=$(extract_review_status "$review_output")
        
        case "$status" in
            APPROVED)
                echo "✓ Phase approved: $phase"
                update_state "$phase" "approved" $cycle
                
                # Commit
                commit_phase "$phase"
                
                return 0
                ;;
            CHANGES_REQUESTED)
                echo "⚠ Changes requested, cycle $cycle/$MAX_CYCLES"
                if [ $cycle -ge $MAX_CYCLES ]; then
                    echo "✗ Max cycles reached, manual intervention needed"
                    echo "Latest feedback:"
                    extract_feedback "$review_output" | head -15
                    update_state "$phase" "needs_manual_fix" $cycle
                    return 1
                fi
                # Continue loop, feedback will be passed to worker
                ;;
            FAILED|"")
                echo "✗ Phase failed: $phase"
                echo "Review output:"
                cat "$review_output" | head -20
                update_state "$phase" "failed" $cycle
                return 1
                ;;
        esac
    done
}

# Execute next phase only
do_next() {
    local phase=$(get_current_phase)
    do_phase "$phase"
}

# Run full orchestration
do_orchestrate() {
    echo "=== Starting Phase-Level Orchestration ==="
    echo ""
    
    mkdir -p "$WORKFLOW_DIR"
    
    # Get starting phase
    local current=$(get_current_phase)
    local start_idx=$(get_phase_index "$current")
    
    echo "Starting from phase: $current"
    echo ""
    
    # Execute phases in order
    for i in $(seq $start_idx $((${#PHASES[@]} - 1))); do
        local phase="${PHASES[$i]}"
        
        if ! do_phase "$phase"; then
            echo ""
            echo "=== Orchestration Stopped ==="
            echo "Phase $phase needs manual intervention"
            return 1
        fi
        
        echo ""
    done
    
    echo "=== Orchestration Complete ==="
    echo "All phases done!"
    
    update_state "COMPLETE" "done" 0
}

# Main
COMMAND="${COMMAND:-orchestrate}"

case "$COMMAND" in
    status)    do_status ;;
    next)      do_next ;;
    init)      
        mkdir -p "$WORKFLOW_DIR"
        update_state "SPECIFY" "init" 0
        echo "Workflow initialized"
        ;;
    orchestrate|*)
        do_orchestrate
        ;;
esac
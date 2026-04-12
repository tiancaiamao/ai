#!/bin/bash
# workflow.sh - Execute workflow phases using ag for agent orchestration
#
# Replaces the old orchestrate CLI backend with ag primitives:
#   ag spawn/wait/output — phase execution
#   ag task — state tracking
#   pair.sh pattern — worker + reviewer (optional)

set -euo pipefail

WORKFLOW_DIR=".workflow"
STATE_FILE="$WORKFLOW_DIR/STATE.json"
SKILL_DIR="${HOME}/.ai/skills/workflow"

# Resolve ag binary
resolve_ag() {
    if [[ -n "${AG_BIN:-}" ]]; then
        echo "$AG_BIN"
        return
    fi
    # Check sibling skill directory
    local candidate="${SKILL_DIR}/../ag/ag"
    if [[ -x "$candidate" ]]; then
        echo "$candidate"
        return
    fi
    # Check PATH
    if command -v ag &>/dev/null; then
        echo "ag"
        return
    fi
    echo "ERROR: ag binary not found. Set AG_BIN or build skills/ag/ag" >&2
    exit 1
}

AG=$(resolve_ag)

usage() {
    cat <<EOF
Usage: workflow.sh <command>

Commands:
    start <template> [description]   Start a workflow
    auto [--no-review] [--phase X]   Execute phases using ag agents
    status                           Show workflow state
    templates                        List available templates
    templates info <name>            Show template details
    pause                            Pause auto mode
    resume                           Resume paused workflow
    stop                             Stop workflow
    next                             Show next phase instructions
    commit                           Commit current phase

Examples:
    workflow.sh start bugfix "login timeout"
    workflow.sh auto
    workflow.sh auto --no-review
    workflow.sh auto --phase implement
    workflow.sh status
EOF
}

# --- State helpers ---

load_state() {
    if [[ ! -f "$STATE_FILE" ]]; then
        echo "No workflow state found. Run 'workflow.sh start' first." >&2
        exit 1
    fi
    cat "$STATE_FILE"
}

get_current_phase() {
    load_state | jq -r '.phases[.currentPhase].name // empty'
}

get_phase_index() {
    load_state | jq -r '.currentPhase'
}

save_state() {
    local state="$1"
    echo "$state" > "$STATE_FILE.tmp" && mv "$STATE_FILE.tmp" "$STATE_FILE"
}

advance_phase() {
    local state
    state=$(load_state)
    local idx
    idx=$(echo "$state" | jq '.currentPhase')
    local total
    total=$(echo "$state" | jq '.phases | length')
    local next=$((idx + 1))

    if [[ $next -ge $total ]]; then
        save_state "$(echo "$state" | jq \
            ".status = \"completed\" | .completedAt = \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\" | \
             .phases[$idx].status = \"completed\"")"
        echo "All phases complete!"
        return 0
    fi

    save_state "$(echo "$state" | jq \
        ".phases[$idx].status = \"completed\" | \
         .currentPhase = $next | \
         .phases[$next].status = \"active\" | \
         .updatedAt = \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"")"
    echo "Advanced to phase: $(get_current_phase)"
}

# --- Phase prompt builder ---

# Build system prompt for a phase worker agent
build_phase_prompt() {
    local phase_name="$1"
    local template="$2"
    local artifact_dir="$3"
    local state="$4"

    local prompt=""

    # Load base worker instructions
    if [[ -f "$SKILL_DIR/references/phase-worker.md" ]]; then
        prompt+="$(cat "$SKILL_DIR/references/phase-worker.md")"
        prompt+=$'\n\n'
    fi

    # Load phase-specific instructions from template
    local template_file="$SKILL_DIR/templates/${template}.md"
    if [[ -f "$template_file" ]]; then
        # Extract the relevant phase section
        local phase_section
        # Phase headers may be "## Phase 1: Spec" or "## Phase 1: spec" etc.
        phase_section=$(sed -n "/^## Phase.*:.*${phase_name}/I,/^## Phase/p" "$template_file" | sed '$d')
        if [[ -n "$phase_section" ]]; then
            prompt+="# Phase Instructions\n\n"
            prompt+="$phase_section"
            prompt+=$'\n\n'
        fi
    fi

    # Add context about previous phases
    local completed
    completed=$(echo "$state" | jq -r '[.phases[] | select(.status == "completed") | .name] | join(", ")')
    if [[ -n "$completed" && "$completed" != "null" ]]; then
        prompt+="# Previously Completed Phases\n\n"
        prompt+="Completed: $completed\n"
        prompt+=$'\n'
    fi

    # Add artifact directory info
    prompt+="# Working Directory\n\n"
    prompt+="Artifact directory: ${artifact_dir}\n"
    prompt+="Write all phase outputs to this directory.\n"

    echo -e "$prompt"
}

# Build context input for the phase worker
build_phase_input() {
    local phase_name="$1"
    local artifact_dir="$2"
    local state="$3"

    local input=""

    input+="Current workflow phase: ${phase_name}\n"
    input+=$'\n'

    # Include description
    local desc
    desc=$(echo "$state" | jq -r '.description // empty')
    if [[ -n "$desc" ]]; then
        input+="Description: ${desc}\n"
        input+=$'\n'
    fi

    # Include previous phase outputs if they exist
    local idx
    idx=$(echo "$state" | jq '.currentPhase')
    if [[ $idx -gt 0 ]]; then
        input+="# Previous Phase Outputs\n\n"
        local prev_idx=$((idx - 1))
        local prev_name
        prev_name=$(echo "$state" | jq -r ".phases[${prev_idx}].name")
        # Look for phase output files
        for f in "$artifact_dir"/*.md; do
            if [[ -f "$f" ]]; then
                local basename
                basename=$(basename "$f")
                # Include files from previous phases (not the template itself)
                if [[ "$basename" != "TEMPLATE.md" ]]; then
                    input+="--- ${basename} ---\n"
                    input+="$(cat "$f")\n\n"
                fi
            fi
        done
    fi

    echo -e "$input"
}

# --- Core ag-based phase execution ---

# Spawn an ag agent for a phase, wait, return output
execute_phase() {
    local phase_name="$1"
    local no_review="${2:-false}"
    local state
    state=$(load_state)
    local template
    template=$(echo "$state" | jq -r '.template')
    local artifact_dir
    artifact_dir=$(echo "$state" | jq -r '.artifactDir')
    local agent_id="wf-${template}-${phase_name}-$$"
    local cwd
    cwd=$(pwd)

    echo "[workflow] === Phase: ${phase_name} ==="

    # Build prompt and input — write to temp files to avoid shell escaping issues
    local system_prompt
    system_prompt=$(build_phase_prompt "$phase_name" "$template" "$artifact_dir" "$state")
    local input_text
    input_text=$(build_phase_input "$phase_name" "$artifact_dir" "$state")

    local system_file
    system_file=$(mktemp /tmp/workflow-system-XXXXXX.md)
    local input_file
    input_file=$(mktemp /tmp/workflow-input-XXXXXX.md)
    echo "$system_prompt" > "$system_file"
    echo "$input_text" > "$input_file"

    # Cleanup function
    cleanup_agent() {
        $AG rm "$agent_id" 2>/dev/null || true
        rm -f "$system_file" "$input_file"
    }

    # --- Worker ---
    local mock_args=""
    if [[ "${AG_MOCK:-}" == "1" ]]; then
        mock_args="--mock"
        if [[ -n "${AG_MOCK_SCRIPT:-}" ]]; then
            mock_args="--mock --mock-script $AG_MOCK_SCRIPT"
        fi
    fi

    echo "[workflow] Spawning worker agent: ${agent_id}"
    if ! $AG spawn \
        --id "$agent_id" \
        --system "$system_file" \
        --input "$input_file" \
        --cwd "$cwd" \
        --timeout 10m \
        $mock_args; then
        echo "[workflow] ❌ Failed to spawn worker for phase: ${phase_name}" >&2
        rm -f "$input_file"
        return 1
    fi

    echo "[workflow] Waiting for worker to complete..."
    if ! $AG wait "$agent_id" --timeout 600; then
        echo "[workflow] ❌ Worker failed or timed out for phase: ${phase_name}" >&2
        local fail_output
        fail_output=$($AG output "$agent_id" 2>/dev/null || echo "No output")
        echo "$fail_output"
        cleanup_agent
        return 1
    fi

    # Capture output
    local output
    output=$($AG output "$agent_id")
    local worker_status
    worker_status=$($AG status "$agent_id" 2>/dev/null | grep "^status:" | awk '{print $2}' || echo "done")

    echo "[workflow] Worker completed (status: ${worker_status})"

    # --- Reviewer (optional pair pattern) ---
    if [[ "$no_review" != "true" && -f "$SKILL_DIR/references/phase-reviewer.md" ]]; then
        local reviewer_id="wf-${template}-${phase_name}-reviewer-$$"

        local review_input_file
        review_input_file=$(mktemp /tmp/workflow-review-XXXXXX.md)
        {
            echo "# Phase Output to Review"
            echo ""
            echo "Phase: ${phase_name}"
            echo "Template: ${template}"
            echo ""
            echo "--- Worker Output ---"
            echo "$output"
        } > "$review_input_file"

        local reviewer_mock_args=""
        if [[ "${AG_MOCK:-}" == "1" ]]; then
            reviewer_mock_args="--mock"
        fi

        echo "[workflow] Spawning reviewer agent: ${reviewer_id}"
        if $AG spawn \
            --id "$reviewer_id" \
            --system "$SKILL_DIR/references/phase-reviewer.md" \
            --input "$review_input_file" \
            --cwd "$cwd" \
            --timeout 5m \
            $reviewer_mock_args; then

            echo "[workflow] Waiting for review..."
            if $AG wait "$reviewer_id" --timeout 300; then
                local review_output
                review_output=$($AG output "$reviewer_id")

                # Check review result — look for APPROVED or CHANGES_REQUESTED
                if echo "$review_output" | grep -qi "APPROVED"; then
                    echo "[workflow] ✅ Review passed"
                elif echo "$review_output" | grep -qi "CHANGES_REQUESTED\|FAILED"; then
                    echo "[workflow] ⚠️  Review requested changes:" >&2
                    echo "$review_output" >&2
                    $AG rm "$reviewer_id" 2>/dev/null || true
                    rm -f "$review_input_file"
                    cleanup_agent
                    return 1
                else
                    echo "[workflow] ℹ️  Review output (no explicit approval/rejection):"
                    echo "$review_output" | head -20
                fi
            else
                echo "[workflow] ⚠️  Reviewer timed out, proceeding anyway"
            fi
            $AG rm "$reviewer_id" 2>/dev/null || true
            rm -f "$review_input_file"
        else
            echo "[workflow] ⚠️  Could not spawn reviewer, proceeding without review"
        fi
    fi

    # Save phase output
    local output_file="${artifact_dir}/${phase_name}-output.md"
    echo "$output" > "$output_file"
    echo "[workflow] Phase output saved: ${output_file}"

    # Cleanup
    cleanup_agent

    echo "[workflow] ✅ Phase ${phase_name} complete"
    return 0
}

# --- Commands ---

cmd_start() {
    local template="${1:-feature}"
    local description="${2:-}"
    local registry="$SKILL_DIR/templates/registry.json"

    if [[ ! -f "$registry" ]]; then
        echo "Registry not found at $registry" >&2
        exit 1
    fi

    local template_data
    template_data=$(jq ".templates[\"$template\"]" "$registry" 2>/dev/null)
    if [[ -z "$template_data" || "$template_data" == "null" ]]; then
        echo "Unknown template: $template" >&2
        echo "Run 'workflow.sh templates' for available templates" >&2
        exit 1
    fi

    local name
    name=$(echo "$template_data" | jq -r '.name')
    local phases_csv
    phases_csv=$(echo "$template_data" | jq -r '.phases | join(", ")')
    local category
    category=$(echo "$template_data" | jq -r '.category')

    # Create directories
    mkdir -p "$WORKFLOW_DIR/$category"

    local date_prefix
    date_prefix=$(date +"%y%m%d")
    local slug
    slug=$(echo "$description" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9]*-/--/g' | head -c 40)
    local artifact_dir="$WORKFLOW_DIR/$category/${date_prefix}-1-${slug:-unnamed}"
    mkdir -p "$artifact_dir"

    # Build phase entries
    local phase_count
    phase_count=$(echo "$template_data" | jq '.phases | length')
    local phase_entries="["
    for i in $(seq 0 $((phase_count - 1))); do
        local pname
        pname=$(echo "$template_data" | jq -r ".phases[$i]")
        local pstatus="pending"
        if [[ $i -eq 0 ]]; then pstatus="active"; fi
        if [[ $i -gt 0 ]]; then phase_entries+=","; fi
        phase_entries+="{\"name\":\"$pname\",\"index\":$i,\"status\":\"$pstatus\"}"
    done
    phase_entries+="]"

    # Create state
    cat > "$STATE_FILE" <<STATEEOF
{
  "template": "$template",
  "templateName": "$name",
  "description": "$description",
  "phases": $phase_entries,
  "currentPhase": 0,
  "status": "in_progress",
  "startedAt": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "artifactDir": "$artifact_dir"
}
STATEEOF

    # Copy template
    cp "$SKILL_DIR/templates/$template.md" "$artifact_dir/TEMPLATE.md"

    echo "Workflow started: $name"
    echo "Description: $description"
    echo "Phases: $phases_csv"
    echo "Artifact dir: $artifact_dir"
    echo ""
    echo "Run 'workflow.sh auto' to execute phases."
}

cmd_auto() {
    local no_review=false
    local target_phase=""

    while [[ $# -gt 0 ]]; do
        case "$1" in
            --no-review) no_review=true; shift ;;
            --phase) target_phase="$2"; shift 2 ;;
            *) shift ;;
        esac
    done

    local state
    state=$(load_state)
    local status
    status=$(echo "$state" | jq -r '.status')

    if [[ "$status" == "completed" ]]; then
        echo "Workflow already completed."
        return 0
    fi

    if [[ "$status" == "paused" ]]; then
        echo "Workflow is paused. Run 'workflow.sh resume' first." >&2
        exit 1
    fi

    # If a specific phase is requested, jump to it
    if [[ -n "$target_phase" ]]; then
        local total
        total=$(echo "$state" | jq '.phases | length')
        local found=false
        for i in $(seq 0 $((total - 1))); do
            local pname
            pname=$(echo "$state" | jq -r ".phases[$i].name")
            if [[ "$pname" == "$target_phase" ]]; then
                # Mark everything before as completed, this one as active
                state=$(echo "$state" | jq "
                    .currentPhase = $i |
                    .phases | map(if .index < $i then .status = \"completed\" elif .index == $i then .status = \"active\" else .status end) as \$p | .phases = \$p
                ")
                save_state "$state"
                found=true
                break
            fi
        done
        if [[ "$found" != "true" ]]; then
            echo "Phase '$target_phase' not found in this workflow." >&2
            exit 1
        fi
    fi

    # Main execution loop
    while true; do
        state=$(load_state)
        status=$(echo "$state" | jq -r '.status')

        if [[ "$status" == "completed" ]]; then
            echo ""
            echo "=== Workflow Complete ==="
            echo "All phases finished successfully."
            return 0
        fi

        if [[ "$status" == "paused" ]]; then
            echo "[workflow] Paused. Run 'workflow.sh resume' to continue."
            return 0
        fi

        local phase
        phase=$(echo "$state" | jq -r '.phases[.currentPhase].name')
        local phase_idx
        phase_idx=$(echo "$state" | jq '.currentPhase')
        local total
        total=$(echo "$state" | jq '.phases | length')

        echo ""
        echo "=== Phase $((phase_idx + 1))/${total}: ${phase} ==="

        if execute_phase "$phase" "$no_review"; then
            advance_phase

            # Reload state after advance
            state=$(load_state)
            status=$(echo "$state" | jq -r '.status')
            if [[ "$status" == "completed" ]]; then
                echo ""
                echo "=== Workflow Complete ==="
                echo "All phases finished successfully."
                return 0
            fi

            echo "[workflow] Moving to next phase..."
        else
            echo "" >&2
            echo "[workflow] ❌ Phase '${phase}' failed." >&2
            echo "[workflow] Fix issues and run 'workflow.sh auto' again." >&2
            return 1
        fi
    done
}

cmd_status() {
    local state
    state=$(load_state)
    local status
    status=$(echo "$state" | jq -r '.status')
    local template
    template=$(echo "$state" | jq -r '.templateName')
    local artifact
    artifact=$(echo "$state" | jq -r '.artifactDir')

    echo "=== Workflow Status ==="
    echo "Template: $template"
    echo "Status:   $status"
    echo "Artifact: $artifact"
    echo ""

    echo "Phases:"
    local total
    total=$(echo "$state" | jq '.phases | length')
    for i in $(seq 0 $((total - 1))); do
        local pname
        pname=$(echo "$state" | jq -r ".phases[$i].name")
        local pstatus
        pstatus=$(echo "$state" | jq -r ".phases[$i].status")
        local marker="○"
        case "$pstatus" in
            completed) marker="✓" ;;
            active)    marker="▶" ;;
            pending)   marker="○" ;;
            failed)    marker="✗" ;;
        esac
        echo "  $marker [$((i + 1))] $pname ($pstatus)"
    done
}

cmd_next() {
    if [[ ! -f "$STATE_FILE" ]]; then
        echo "No active workflow. Run 'workflow.sh start' first." >&2
        exit 1
    fi

    local phase
    phase=$(get_current_phase)
    local state
    state=$(load_state)
    local artifact
    artifact=$(echo "$state" | jq -r '.artifactDir')
    local template
    template=$(echo "$state" | jq -r '.template')

    echo "=== Next Phase: ${phase} ==="
    echo "Artifact dir: $artifact"
    echo ""
    echo "Run 'workflow.sh auto' to execute this phase with an ag agent."
    echo "Run 'workflow.sh auto --no-review' to skip the review step."
}

cmd_commit() {
    local phase
    phase=$(get_current_phase)
    local template
    template=$(load_state | jq -r '.template')

    git add -A
    git commit -m "feat(${template}): complete ${phase} phase" || echo "Nothing to commit"
    echo "Phase committed"
}

cmd_pause() {
    if [[ ! -f "$STATE_FILE" ]]; then
        echo "No active workflow." >&2
        exit 1
    fi
    local state
    state=$(cat "$STATE_FILE")
    local status
    status=$(echo "$state" | jq -r '.status')
    if [[ "$status" == "paused" ]]; then
        echo "Already paused."
        return
    fi
    save_state "$(echo "$state" | jq '.status = "paused" | .pausedAt = "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'"')"
    echo "Workflow paused."
}

cmd_resume() {
    if [[ ! -f "$STATE_FILE" ]]; then
        echo "No active workflow." >&2
        exit 1
    fi
    local state
    state=$(cat "$STATE_FILE")
    local status
    status=$(echo "$state" | jq -r '.status')
    if [[ "$status" != "paused" ]]; then
        echo "Workflow is not paused (status: $status)." >&2
        return 1
    fi
    save_state "$(echo "$state" | jq '.status = "in_progress" | .resumedAt = "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'"')"
    local phase
    phase=$(get_current_phase)
    echo "Workflow resumed. Current phase: $phase"
    cmd_status
}

cmd_stop() {
    if [[ ! -f "$STATE_FILE" ]]; then
        echo "No active workflow."
        return
    fi
    read -p "Stop workflow and remove .workflow directory? [y/N] " confirm
    if [[ "$confirm" =~ ^[Yy]$ ]]; then
        rm -rf "$WORKFLOW_DIR"
        echo "Workflow stopped and removed."
    else
        echo "Cancelled."
    fi
}

list_templates() {
    local registry="$SKILL_DIR/templates/registry.json"
    if [[ ! -f "$registry" ]]; then
        echo "Registry not found at $registry" >&2
        exit 1
    fi
    echo "Available templates:"
    echo ""
    jq -r '.templates | to_entries[] | "- \(.key): \(.value.name) (\(.value.complexity)) — \(.value.description)"' "$registry"
}

template_info() {
    local name="$1"
    local registry="$SKILL_DIR/templates/registry.json"
    if [[ ! -f "$registry" ]]; then
        echo "Registry not found" >&2
        exit 1
    fi
    jq ".templates[\"$name\"]" "$registry" 2>/dev/null || echo "Template '$name' not found"
}

# --- Main dispatch ---

case "${1:-}" in
    start)
        shift
        cmd_start "$@"
        ;;
    auto)
        shift
        cmd_auto "$@"
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
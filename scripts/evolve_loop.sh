#!/usr/bin/env bash
# evolve_loop.sh — Automated evolve loop for agent harness optimization.
#
# Usage:
#   evolve_loop.sh \
#     [--manifest PATH] \
#     [--agent-config PATH] \
#     [--max-iterations N] \
#     [--timeout DURATION] \
#     [--evolve-dir PATH] \
#     [--start-iteration N]
#
# Defaults:
#   manifest:       agent/benchmarks/evolve/evolve-manifest.json
#   agent-config:   agent/agent.yaml
#   max-iterations: 5
#   timeout:        10m
#   evolve-dir:     agent/benchmarks/evolve
#   start-iteration: (none — auto-detect from existing artifacts)

set -euo pipefail

# ─── Project root ─────────────────────────────────────────────────────────────
# Resolve project root: directory containing this script's parent (scripts/)
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# ─── Defaults ─────────────────────────────────────────────────────────────────
MANIFEST="${PROJECT_ROOT}/agent/benchmarks/evolve/evolve-manifest.json"
AGENT_CONFIG="${PROJECT_ROOT}/agent/agent.yaml"
MAX_ITERATIONS=5
TIMEOUT="10m"
EVOLVE_DIR="${PROJECT_ROOT}/agent/benchmarks/evolve"
START_ITERATION=""
AI_BIN="/Users/genius/go/bin/ai"
BENCHMARK_BIN="${PROJECT_ROOT}/bin/benchmark"
SCRIPTS_DIR="${PROJECT_ROOT}/benchmark/scripts"
TEMPLATE="${PROJECT_ROOT}/agent/benchmarks/evolve-planner-input-template.md"

# ─── Parse arguments ──────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
    case "$1" in
        --manifest)
            MANIFEST="$2"; shift 2 ;;
        --agent-config)
            AGENT_CONFIG="$2"; shift 2 ;;
        --max-iterations)
            MAX_ITERATIONS="$2"; shift 2 ;;
        --timeout)
            TIMEOUT="$2"; shift 2 ;;
        --evolve-dir)
            EVOLVE_DIR="$2"; shift 2 ;;
        --start-iteration)
            START_ITERATION="$2"; shift 2 ;;
        -h|--help)
            sed -n '2,/^$/p' "$0" | sed 's/^# //' | sed 's/^#//'
            exit 0
            ;;
        *)
            echo "ERROR: Unknown argument: $1" >&2
            exit 1
            ;;
    esac
done

# Resolve to absolute paths
MANIFEST="$(cd "$(dirname "$MANIFEST")" && pwd)/$(basename "$MANIFEST")"
AGENT_CONFIG="$(cd "$(dirname "$AGENT_CONFIG")" && pwd)/$(basename "$AGENT_CONFIG")"
EVOLVE_DIR="$(cd "$(dirname "$EVOLVE_DIR")" && pwd)/$(basename "$EVOLVE_DIR")"

# ─── Pre-flight checks ────────────────────────────────────────────────────────
echo "=== Evolve Loop — Pre-flight Checks ==="

for tool in "${AI_BIN}" "${BENCHMARK_BIN}"; do
    if [[ ! -x "$tool" ]]; then
        echo "ERROR: Required binary not found or not executable: $tool" >&2
        exit 1
    fi
done

for script in "${SCRIPTS_DIR}/trace_normalizer.py" "${SCRIPTS_DIR}/agent_debugger.py" "${SCRIPTS_DIR}/build_planner_context.py"; do
    if [[ ! -f "$script" ]]; then
        echo "ERROR: Required script not found: $script" >&2
        exit 1
    fi
done

if [[ ! -f "$MANIFEST" ]]; then
    echo "ERROR: Manifest not found: $MANIFEST" >&2
    exit 1
fi

if [[ ! -f "$AGENT_CONFIG" ]]; then
    echo "ERROR: Agent config not found: $AGENT_CONFIG" >&2
    exit 1
fi

if [[ ! -f "$TEMPLATE" ]]; then
    echo "ERROR: Planner input template not found: $TEMPLATE" >&2
    exit 1
fi

for cmd in jq python3; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "ERROR: Required command not found: $cmd" >&2
        exit 1
    fi
done

echo "  manifest:        ${MANIFEST}"
echo "  agent-config:    ${AGENT_CONFIG}"
echo "  max-iterations:  ${MAX_ITERATIONS}"
echo "  timeout:         ${TIMEOUT}"
echo "  evolve-dir:      ${EVOLVE_DIR}"
echo "  template:        ${TEMPLATE}"
echo "  start-iteration: ${START_ITERATION:-auto}"
echo ""

# ─── State file helpers ───────────────────────────────────────────────────────

function init_state() {
    local STATE_FILE="${EVOLVE_DIR}/state.json"
    if [[ ! -f "$STATE_FILE" ]]; then
        cat > "$STATE_FILE" <<'EOF'
{
  "current_iteration": -1,
  "best_pass_rate": 0,
  "best_iteration": -1,
  "total_iterations": 0,
  "history": []
}
EOF
        echo "[state] Initialized state.json"
    fi
}

function update_state() {
    local ITER=$1
    local STATE_FILE="${EVOLVE_DIR}/state.json"
    local RESULT_FILE="${EVOLVE_DIR}/iteration-${ITER}.json"

    if [[ ! -f "$RESULT_FILE" ]]; then
        echo "ERROR: Result file not found for iteration ${ITER}: ${RESULT_FILE}" >&2
        exit 1
    fi

    local CURRENT_RATE
    CURRENT_RATE=$(jq '.pass_rate' "$RESULT_FILE")

    python3 -c "
import json, sys

state_file = '${STATE_FILE}'
result_file = '${RESULT_FILE}'
current_iter = ${ITER}
current_rate = ${CURRENT_RATE}

with open(state_file) as f:
    state = json.load(f)

state['current_iteration'] = current_iter

if current_rate > state.get('best_pass_rate', 0):
    state['best_pass_rate'] = current_rate
    state['best_iteration'] = current_iter

state['total_iterations'] = current_iter + 1

# Append to history (delta vs. previous iteration)
prev_rate = state['history'][-1]['pass_rate'] if state.get('history') else 0
delta = round(current_rate - prev_rate, 4)

entry = {
    'iteration': current_iter,
    'pass_rate': current_rate,
    'delta': delta,
    'timestamp': '$(date -u +%Y-%m-%dT%H:%M:%SZ)',
}
history = state.get('history', [])
history.append(entry)
state['history'] = history

with open(state_file, 'w') as f:
    json.dump(state, f, indent=2)
print(f'[state] Updated: iter={current_iter}, rate={current_rate}, best={state[\"best_pass_rate\"]}')
"
}

# ─── Create debugger config ───────────────────────────────────────────────────

function create_debugger_config() {
    local CONFIG_PATH="${EVOLVE_DIR}/debugger_config.yaml"
    cat > "$CONFIG_PATH" <<'EOF'
timeout: 300
EOF
    echo "$CONFIG_PATH"
}








# ─── Accept/Reject ────────────────────────────────────────────────────────────

function update_decision_in_state() {
    local ITER=$1
    local ACTION=$2
    local STATE_FILE="${EVOLVE_DIR}/state.json"

    python3 -c "
import json, os

state_file = '${STATE_FILE}'
current_iter = ${ITER}
action = '${ACTION}'
evolve_dir = '${EVOLVE_DIR}'

with open(state_file) as f:
    state = json.load(f)

for entry in state.get('history', []):
    if entry.get('iteration') == current_iter:
        entry['decision'] = action
        # Also save attribution info in state history
        attr_file = os.path.join(evolve_dir, f'attribution-{current_iter}.json')
        if os.path.exists(attr_file):
            with open(attr_file) as f:
                attr = json.load(f)
            entry['target'] = attr.get('target', 'unknown')
            entry['change_description'] = attr.get('change_description', '')
            entry['predicted_fixes'] = attr.get('predicted_fixes', [])
        break

with open(state_file, 'w') as f:
    json.dump(state, f, indent=2)
print(f'[state] Decision updated: iter={current_iter}, decision={action}')
"
}

function accept_or_reject() {
    local ITER=$1
    local ARTIFACTS_DIR=$2

    local RESULT_FILE="${EVOLVE_DIR}/iteration-${ITER}.json"
    local NEW_PASS_RATE
    NEW_PASS_RATE=$(jq '.pass_rate' "$RESULT_FILE")

        if [[ "$ITER" -eq 0 ]]; then
        echo "  [decision] Iteration 0 (baseline): pass_rate=${NEW_PASS_RATE}"
        echo "{\"action\": \"baseline\", \"pass_rate\": ${NEW_PASS_RATE}}" > "${ARTIFACTS_DIR}/decision.json"
        update_decision_in_state "$ITER" "baseline"
        return 0
    fi

    local PREV_ITER=$((ITER - 1))
    local PREV_RESULT="${EVOLVE_DIR}/iteration-${PREV_ITER}.json"

    if [[ ! -f "$PREV_RESULT" ]]; then
                echo "  [decision] WARN: Previous iteration result not found (${PREV_RESULT}), accepting by default"
        echo "{\"action\": \"accept\", \"prev_rate\": null, \"new_rate\": ${NEW_PASS_RATE}}" > "${ARTIFACTS_DIR}/decision.json"
        update_decision_in_state "$ITER" "accept"
        return 0
    fi

    local PREV_PASS_RATE
    PREV_PASS_RATE=$(jq '.pass_rate' "$PREV_RESULT")

    local IMPROVED
    IMPROVED=$(python3 -c "print('yes' if ${NEW_PASS_RATE} >= ${PREV_PASS_RATE} else 'no')")

        if [[ "$IMPROVED" == "yes" ]]; then
        echo "  [decision] ACCEPT: ${PREV_PASS_RATE} -> ${NEW_PASS_RATE}"
        # Apply new config if available and is real YAML (not a tool-edit marker)
        if [[ -f "${ARTIFACTS_DIR}/config.yaml" ]] && [[ -s "${ARTIFACTS_DIR}/config.yaml" ]]; then
            local FIRST_LINE
            FIRST_LINE=$(head -1 "${ARTIFACTS_DIR}/config.yaml")
            if [[ "${FIRST_LINE}" == "# Planner made tool-based edits"* ]]; then
                echo "  [decision] Planner made tool-based edits (system_prompt.md/memory.md) — config.yaml unchanged"
            else
                cp "${ARTIFACTS_DIR}/config.yaml" "${AGENT_CONFIG}"
                echo "  [decision] New config applied to ${AGENT_CONFIG}"
            fi
        else
            echo "  [decision] WARN: No valid config.yaml found in artifacts, keeping current config"
        fi
                echo "{\"action\": \"accept\", \"prev_rate\": ${PREV_PASS_RATE}, \"new_rate\": ${NEW_PASS_RATE}}" \
            > "${ARTIFACTS_DIR}/decision.json"
        update_decision_in_state "$ITER" "accept"
    else
        echo "  [decision] REJECT: ${PREV_PASS_RATE} -> ${NEW_PASS_RATE} (regression)"
                # Rollback config
        if [[ -f "${ARTIFACTS_DIR}/config-backup.yaml" ]]; then
            cp "${ARTIFACTS_DIR}/config-backup.yaml" "${AGENT_CONFIG}"
            echo "  [decision] Config rolled back from backup"
        else
            echo "  [decision] WARN: No backup config found for rollback"
        fi
        # Rollback harness files (system_prompt.md, memory.md etc.)
        local AGENT_DIR_ROLLBACK
        AGENT_DIR_ROLLBACK=$(dirname "${AGENT_CONFIG}")
        for hf in system_prompt.md memory.md context_management.md; do
            if [[ -f "${ARTIFACTS_DIR}/${hf}.backup" ]]; then
                cp "${ARTIFACTS_DIR}/${hf}.backup" "${AGENT_DIR_ROLLBACK}/${hf}"
                echo "  [decision] Rolled back ${hf}"
            fi
        done
                echo "{\"action\": \"reject\", \"prev_rate\": ${PREV_PASS_RATE}, \"new_rate\": ${NEW_PASS_RATE}}" \
            > "${ARTIFACTS_DIR}/decision.json"
        update_decision_in_state "$ITER" "reject"
        fi
}

# ─── Single iteration ─────────────────────────────────────────────────────────

function run_iteration() {
    local ITER=$1
    local START_TS
    START_TS=$(date +%s)

    echo ""
    echo "================================================================"
    echo "  Iteration ${ITER}"
    echo "================================================================"

    # 0. Prepare artifacts directory
    local ARTIFACTS_DIR="${EVOLVE_DIR}/iter-${ITER}-artifacts"
    mkdir -p "${ARTIFACTS_DIR}/traces"

        # 1. Backup current config and harness files (for rollback)
    cp "${AGENT_CONFIG}" "${ARTIFACTS_DIR}/config-backup.yaml"
    echo "  [1/7] Config backed up -> ${ARTIFACTS_DIR}/config-backup.yaml"
    # Also backup harness files that planner may edit via tools
    local AGENT_DIR
    AGENT_DIR=$(dirname "${AGENT_CONFIG}")
    for hf in system_prompt.md memory.md context_management.md; do
        if [[ -f "${AGENT_DIR}/${hf}" ]]; then
            cp "${AGENT_DIR}/${hf}" "${ARTIFACTS_DIR}/${hf}.backup"
        fi
    done

    # 2. Run benchmark
    echo "  [2/7] Running benchmark (timeout=${TIMEOUT})..."
    local BENCH_START
    BENCH_START=$(date +%s)

    (
        cd "${PROJECT_ROOT}"
        "${BENCHMARK_BIN}" \
            --tasks "${PROJECT_ROOT}/benchmark/tasks" \
            --agent-config "${AGENT_CONFIG}" \
            --manifest "${MANIFEST}" \
            --agent "${AI_BIN}" \
            --timeout "${TIMEOUT}" \
            --clean
    )

    local BENCH_EXIT=$?
    local BENCH_END
    BENCH_END=$(date +%s)
    local BENCH_DURATION=$((BENCH_END - BENCH_START))

    if [[ $BENCH_EXIT -ne 0 ]]; then
        echo "  [2/7] ERROR: Benchmark failed (exit=${BENCH_EXIT}, duration=${BENCH_DURATION}s)" >&2
        echo "{\"action\": \"error\", \"step\": \"benchmark\", \"exit_code\": ${BENCH_EXIT}}" > "${ARTIFACTS_DIR}/decision.json"
        exit 1
    fi
    echo "  [2/7] Benchmark completed in ${BENCH_DURATION}s"

    # Find latest result file
        local RESULT_FILE
    RESULT_FILE=$(find "${PROJECT_ROOT}/results" -name 'result_*.json' -maxdepth 1 -type f -print0 2>/dev/null | xargs -0 ls -t 2>/dev/null | head -1)
    if [[ -z "$RESULT_FILE" ]]; then
        echo "  [2/7] ERROR: No result file found in results/" >&2
        exit 1
    fi

    # Save as iteration-N.json
    cp "${RESULT_FILE}" "${EVOLVE_DIR}/iteration-${ITER}.json"
    local PASS_RATE
    PASS_RATE=$(jq '.pass_rate' "${EVOLVE_DIR}/iteration-${ITER}.json")
    local PASSED
    PASSED=$(jq '.passed' "${EVOLVE_DIR}/iteration-${ITER}.json")
    local TOTAL
    TOTAL=$(jq '.total_tasks' "${EVOLVE_DIR}/iteration-${ITER}.json")
    echo "  [2/7] Results: ${PASSED}/${TOTAL} passed (${PASS_RATE}%)"

        # 3. Extract trajectories
    echo "  [3/7] Normalizing traces..."
    if ! python3 "${SCRIPTS_DIR}/trace_normalizer.py" \
        --input "${EVOLVE_DIR}/iteration-${ITER}.json" \
        --output-dir "${ARTIFACTS_DIR}/traces" \
        --all; then
        echo "  [3/7] WARN: Trace normalization had errors (continuing)"
    fi

    local TRACE_COUNT
    TRACE_COUNT=$(find "${ARTIFACTS_DIR}/traces" -name '*.json' 2>/dev/null | wc -l | tr -d ' ')
    echo "  [3/7] Generated ${TRACE_COUNT} trace files"

    # 4. Debugger analysis (failed tasks only)
    echo "  [4/7] Running debugger analysis on failed tasks..."
    local DEBUGGER_START
    DEBUGGER_START=$(date +%s)

    local FAILED_TRACES=""
    for task_id in $(jq -r '.results[] | select(.passed == false) | .task_id' "${EVOLVE_DIR}/iteration-${ITER}.json" 2>/dev/null); do
        local safe_name
        safe_name=$(echo "$task_id" | tr '/' '_')
        local trace_file="${ARTIFACTS_DIR}/traces/${safe_name}-rollout-0.normalized.json"
        if [[ -f "$trace_file" ]]; then
            FAILED_TRACES="${FAILED_TRACES} ${trace_file}"
        fi
    done

    local DEBUGGER_CONFIG
    DEBUGGER_CONFIG=$(create_debugger_config)

        if [[ -n "${FAILED_TRACES// /}" ]]; then
        # shellcheck disable=SC2086
        python3 "${SCRIPTS_DIR}/agent_debugger.py" check \
            --traces ${FAILED_TRACES} \
            --config "${DEBUGGER_CONFIG}" \
            --output "${ARTIFACTS_DIR}/debugger-analysis.json" \
            --format json \
            2>"${ARTIFACTS_DIR}/debugger-stderr.log" \
            || echo "  [4/7] WARN: Debugger analysis failed (see debugger-stderr.log)"
    else
        echo "  [4/7] No failed tasks — skipping debugger"
        echo '{"analysis": "no_failed_tasks"}' > "${ARTIFACTS_DIR}/debugger-analysis.json"
    fi

    local DEBUGGER_END
    DEBUGGER_END=$(date +%s)
    echo "  [4/7] Debugger completed in $((DEBUGGER_END - DEBUGGER_START))s"

        # 5. Update state and build planner context
    echo "  [5/7] Building planner context..."
    update_state "$ITER"

    # Build task_history.json from all existing iteration results
    python3 << 'PYEOF'
import json, glob, os

evolve_dir = os.environ.get('EVOLVE_DIR', '.')
output_file = os.path.join(evolve_dir, 'task_history.json')

files = sorted(glob.glob(os.path.join(evolve_dir, 'iteration-*.json')))
task_history = {}

for f in files:
    try:
        with open(f) as fh:
            data = json.load(fh)
        iter_num = int(os.path.basename(f).split('iteration-')[1].split('.')[0])
        for r in data.get('results', []):
            tid = r.get('task_id', 'unknown')
            passed = r.get('passed', False)
            verdict = 'pass' if passed else 'fail'
            task_history.setdefault(tid, []).append([iter_num, verdict])
    except Exception:
        pass

with open(output_file, 'w') as fh:
    json.dump(task_history, fh, indent=2)
PYEOF

                # Build extra args from previous iteration attribution
        local CONTEXT_EXTRA_ARGS=""
        local PREV_ITER=$((ITER - 1))
        if [[ "${PREV_ITER}" -ge 0 ]] && [[ -f "${EVOLVE_DIR}/attribution-${PREV_ITER}.json" ]]; then
            CONTEXT_EXTRA_ARGS="${CONTEXT_EXTRA_ARGS} --attribution ${EVOLVE_DIR}/attribution-${PREV_ITER}.json"
        fi
        if [[ "${PREV_ITER}" -ge 0 ]] && [[ -f "${EVOLVE_DIR}/attribution-eval-${PREV_ITER}.json" ]]; then
            CONTEXT_EXTRA_ARGS="${CONTEXT_EXTRA_ARGS} --attribution-eval ${EVOLVE_DIR}/attribution-eval-${PREV_ITER}.json"
        fi

        if ! python3 "${SCRIPTS_DIR}/build_planner_context.py" \
        --baseline "${EVOLVE_DIR}/iteration-0.json" \
        --current-result "${EVOLVE_DIR}/iteration-${ITER}.json" \
        --config-yaml "${AGENT_CONFIG}" \
        --state "${EVOLVE_DIR}/state.json" \
        --task-history "${EVOLVE_DIR}/task_history.json" \
        --debugger-analysis "${ARTIFACTS_DIR}/debugger-analysis.json" \
        --template "${TEMPLATE}" \
        --output "${ARTIFACTS_DIR}/planner-input.md" \
        $CONTEXT_EXTRA_ARGS; then
        echo "  [5/7] ERROR: Failed to build planner context" >&2
        exit 1
    fi
    echo "  [5/7] Planner context written to ${ARTIFACTS_DIR}/planner-input.md"

            # 6. Call Planner Agent via ai rpc, pipe through filter
    echo "  [6/7] Calling planner agent..."
    local PLANNER_START
    PLANNER_START=$(date +%s)

    # Build JSON-RPC input: read planner-input.md and wrap as prompt message
    python3 -c "
import json, sys
prompt = open('${ARTIFACTS_DIR}/planner-input.md').read()
rpc_msg = json.dumps({'type': 'prompt', 'message': prompt})
print(rpc_msg)
" | "${AI_BIN}" --mode rpc \
        --agent-config "${AGENT_CONFIG}" \
        --system-prompt "@${PROJECT_ROOT}/docs/design/planner-system-prompt.md" \
        2>"${ARTIFACTS_DIR}/planner-stderr.log" \
        | python3 "${PROJECT_ROOT}/scripts/planner_rpc_filter.py" \
        --iteration "${ITER}" \
        --summary-output "${ARTIFACTS_DIR}/planner-summary.md" \
        --result-output "${ARTIFACTS_DIR}/planner-result.json"

    local PLANNER_EXIT=${PIPESTATUS[1]}
    local PLANNER_END
    PLANNER_END=$(date +%s)

    if [[ $PLANNER_EXIT -ne 0 ]]; then
        echo "  [6/7] WARN: Planner agent exited with code ${PLANNER_EXIT} (see planner-stderr.log)"
    else
                echo "  [6/7] Planner completed in $((PLANNER_END - PLANNER_START))s"
    fi

        # --- Step 6.5: Extract attribution from planner result ---
    if [[ -f "${ARTIFACTS_DIR}/planner-result.json" ]]; then
        ARTIFACTS_DIR="${ARTIFACTS_DIR}" EVOLVE_DIR="${EVOLVE_DIR}" ITER="${ITER}" \
        python3 << 'ATTR_EXTRACT_EOF'
import json, sys, os

artifacts_dir = os.environ.get('ARTIFACTS_DIR', '')
evolve_dir = os.environ.get('EVOLVE_DIR', '')
iter_num = int(os.environ.get('ITER', '0'))

planner_file = os.path.join(artifacts_dir, 'planner-result.json')
with open(planner_file) as f:
    data = json.load(f)

cp = data.get('change_plan')

if cp:
    cp['iteration'] = iter_num
    # Save to EVOLVE_DIR for cross-iteration access
    attr_path = os.path.join(evolve_dir, f'attribution-{iter_num}.json')
    with open(attr_path, 'w') as f:
        json.dump(cp, f, indent=2)
    print(f'  [6.5/7] Attribution saved: {attr_path}')
else:
    print('  [6.5/7] WARN: No Change Plan found in planner output')
    attr_path = os.path.join(evolve_dir, f'attribution-{iter_num}.json')
    with open(attr_path, 'w') as f:
        json.dump({
            'iteration': iter_num,
            'target': 'unknown',
            'predicted_fixes': [],
            'predicted_risks': [],
            'rationale': 'No change plan provided by planner',
            'change_description': 'unknown'
        }, f, indent=2)
ATTR_EXTRACT_EOF
    fi

    # 7. Extract config from planner result
    echo "  [7/7] Processing planner result..."
    local PLANNER_RESULT_FILE="${ARTIFACTS_DIR}/planner-result.json"
    if [[ -f "$PLANNER_RESULT_FILE" ]] && [[ -s "$PLANNER_RESULT_FILE" ]]; then
        local PLANNER_TOOL_EDITS
        PLANNER_TOOL_EDITS=$(jq -r '.tool_edits | join(", ")' "$PLANNER_RESULT_FILE" 2>/dev/null)
        local PLANNER_HAS_YAML
        PLANNER_HAS_YAML=$(jq '.yaml_config != null' "$PLANNER_RESULT_FILE" 2>/dev/null)
        local PLANNER_HAS_CHANGES
        PLANNER_HAS_CHANGES=$(jq '.has_changes' "$PLANNER_RESULT_FILE" 2>/dev/null)

        if [[ "$PLANNER_HAS_YAML" == "true" ]]; then
            jq -r '.yaml_config' "$PLANNER_RESULT_FILE" > "${ARTIFACTS_DIR}/config.yaml"
            echo "  [7/7] Config extracted (YAML block from planner text)"
        elif [[ -n "$PLANNER_TOOL_EDITS" ]]; then
            echo "# Planner made tool-based edits (no YAML block)" > "${ARTIFACTS_DIR}/config.yaml"
            echo "  [7/7] Planner made tool-based edits ($PLANNER_TOOL_EDITS)"
        else
            echo "  [7/7] WARN: No config changes from planner — keeping current config"
        fi
        else
        echo "  [7/7] WARN: No planner result found — keeping current config"
    fi

    # --- Step 7.5: Check for multi-target changes ---
    if [[ -f "${ARTIFACTS_DIR}/planner-result.json" ]]; then
        local MULTI_TARGET_CHECK
        MULTI_TARGET_CHECK=$(ARTIFACTS_DIR="${ARTIFACTS_DIR}" python3 << 'MULTI_TARGET_EOF'
import json, sys, os

planner_file = os.path.join(os.environ.get('ARTIFACTS_DIR', ''), 'planner-result.json')
try:
    with open(planner_file) as f:
        data = json.load(f)
    targets = set()
    for edit in data.get('tool_edits', []):
        path = edit.get('file_path', '')
        if 'system_prompt.md' in path:
            targets.add('system_prompt.md')
        elif 'memory.md' in path:
            targets.add('memory.md')
        elif 'context_management.md' in path:
            targets.add('context_management.md')
        elif 'agent.yaml' in path or path.endswith('config.yaml'):
            targets.add('agent.yaml')
    if len(targets) > 1:
        print(f'WARN: Planner edited {len(targets)} targets: {targets}')
        sys.exit(1)
    else:
        single = list(targets)[0] if targets else 'none'
        print(f'OK: Single target: {single}')
        sys.exit(0)
except Exception as e:
    print(f'WARN: Could not check targets: {e}')
    sys.exit(0)
MULTI_TARGET_EOF
        )
        if [[ $? -ne 0 ]]; then
            echo "  [7.5/7] WARNING: ${MULTI_TARGET_CHECK}"
            echo "  [7.5/7] Multi-target changes detected — consider constraining planner"
        else
            echo "  [7.5/7] ${MULTI_TARGET_CHECK}"
        fi
    fi

    # --- Generate attribution evaluation (benchmark results available) ---
    if [[ -f "${EVOLVE_DIR}/attribution-${ITER}.json" ]]; then
        ITER="${ITER}" EVOLVE_DIR="${EVOLVE_DIR}" ARTIFACTS_DIR="${ARTIFACTS_DIR}" \
        python3 << 'ATTR_EVAL_EOF'
import json, sys, os

iter_num = int(os.environ.get('ITER', '0'))
evolve_dir = os.environ.get('EVOLVE_DIR', '')
artifacts_dir = os.environ.get('ARTIFACTS_DIR', '')

# Load attribution
with open(os.path.join(evolve_dir, f'attribution-{iter_num}.json')) as f:
    attr = json.load(f)

predicted_fixes = attr.get('predicted_fixes', [])
predicted_risks = attr.get('predicted_risks', [])

# Load current and previous results
cur_file = os.path.join(evolve_dir, f'iteration-{iter_num}.json')
prev_iter = iter_num - 1
prev_file = os.path.join(evolve_dir, f'iteration-{prev_iter}.json') if prev_iter >= 0 else None

def get_task_results(filepath):
    if not filepath or not os.path.exists(filepath):
        return {}
    try:
        with open(filepath) as f:
            data = json.load(f)
        results = {}
        for t in data.get('results', []):
            results[t['task_id']] = 'pass' if t.get('passed') else 'fail'
        return results
    except Exception:
        return {}

cur_results = get_task_results(cur_file)
prev_results = get_task_results(prev_file)

# Evaluate predictions
actually_fixed = []
for task in predicted_fixes:
    if cur_results.get(task) == 'pass' and prev_results.get(task) != 'pass':
        actually_fixed.append(task)

failed_to_fix = [t for t in predicted_fixes if t not in actually_fixed]

actual_regressions = []
for task in predicted_risks:
    if cur_results.get(task) != 'pass' and prev_results.get(task) == 'pass':
        actual_regressions.append(task)

# Unexpected regressions
unexpected = []
for task, status in prev_results.items():
    if status == 'pass' and cur_results.get(task) != 'pass' and task not in predicted_risks:
        unexpected.append(task)

# Verdict
if not predicted_fixes:
    verdict = "NO_PLAN"
elif len(actually_fixed) == len(predicted_fixes) and not actual_regressions and not unexpected:
    verdict = "SUCCESS"
elif actually_fixed and not actual_regressions and not unexpected:
    verdict = "PARTIAL"
elif actual_regressions or unexpected:
    verdict = "HARMFUL"
else:
    verdict = "INEFFECTIVE"

eval_data = {
    'iteration': iter_num,
    'verdict': verdict,
    'predicted_fixes': predicted_fixes,
    'actually_fixed': actually_fixed,
    'failed_to_fix': failed_to_fix,
    'predicted_risks': predicted_risks,
    'actual_regressions': actual_regressions,
    'unexpected_regressions': unexpected,
    'fix_rate': f"{len(actually_fixed)}/{len(predicted_fixes)}" if predicted_fixes else "N/A"
}

eval_path = os.path.join(evolve_dir, f'attribution-eval-{iter_num}.json')
with open(eval_path, 'w') as f:
    json.dump(eval_data, f, indent=2)

print(f'  Attribution eval: {verdict} (fixed {len(actually_fixed)}/{len(predicted_fixes)} predicted, {len(actual_regressions)} regressed)')
ATTR_EVAL_EOF
    fi

    # ─── Accept / Reject ──────────────────────────────────────────────────
    accept_or_reject "$ITER" "$ARTIFACTS_DIR"

    # ─── Summary ──────────────────────────────────────────────────────────
    local END_TS
    END_TS=$(date +%s)
    local TOTAL_DURATION=$((END_TS - START_TS))
    echo ""
    echo "  ── Iteration ${ITER} Summary ──"
    echo "  Pass rate: ${PASS_RATE}%"
    echo "  Duration:  ${TOTAL_DURATION}s"
    echo "  Artifacts: ${ARTIFACTS_DIR}/"
    echo "================================================================"
}

# ─── Main ─────────────────────────────────────────────────────────────────────

echo "=== Evolve Loop Starting ==="
echo "  Project root:  ${PROJECT_ROOT}"
echo "  Max iterations: ${MAX_ITERATIONS}"
echo ""

init_state

# Determine start iteration
FIRST_ITER=1
if [[ -n "$START_ITERATION" ]]; then
    FIRST_ITER="$START_ITERATION"
    echo "[main] Resuming from iteration ${FIRST_ITER}"
elif [[ ! -f "${EVOLVE_DIR}/iteration-0.json" ]]; then
    # No baseline yet — run iteration 0
    echo "[main] No baseline found, running iteration 0..."
    run_iteration 0
    FIRST_ITER=1
else
    echo "[main] Baseline exists (iteration-0.json), skipping baseline run"
    # Auto-detect next iteration from existing files
    FIRST_ITER=$(python3 -c "
import glob
files = glob.glob('${EVOLVE_DIR}/iteration-*.json')
if not files:
    print(1)
else:
    nums = [int(f.split('iteration-')[1].split('.')[0]) for f in files]
    print(max(nums) + 1)
")
    echo "[main] Detected last iteration $((FIRST_ITER - 1)), starting from ${FIRST_ITER}"
fi

# ─── Iteration loop ───────────────────────────────────────────────────────────

CONSECUTIVE_REJECTS=0
LAST_ITER=$((FIRST_ITER + MAX_ITERATIONS - 1))

for ITER in $(seq "$FIRST_ITER" "$LAST_ITER"); do
    run_iteration "$ITER"

    # Check decision
    DECISION_FILE="${EVOLVE_DIR}/iter-${ITER}-artifacts/decision.json"
    if [[ -f "$DECISION_FILE" ]]; then
        ACTION=$(jq -r '.action' "$DECISION_FILE")

        if [[ "$ACTION" == "reject" ]]; then
            CONSECUTIVE_REJECTS=$((CONSECUTIVE_REJECTS + 1))
            echo "[main] Consecutive rejects: ${CONSECUTIVE_REJECTS}"

            if [[ $CONSECUTIVE_REJECTS -ge 3 ]]; then
                echo ""
                echo "=== Stopping: 3 consecutive rejects ==="
                break
            fi
        else
            CONSECUTIVE_REJECTS=0
        fi

        if [[ "$ACTION" == "error" ]]; then
            echo "[main] ERROR: Iteration ${ITER} had an error, stopping"
            exit 1
        fi
    fi
done

# ─── Final summary ────────────────────────────────────────────────────────────

echo ""
echo "=== Evolve Loop Complete ==="

STATE_FILE="${EVOLVE_DIR}/state.json"
if [[ -f "$STATE_FILE" ]]; then
    BEST_RATE=$(jq -r '.best_pass_rate' "$STATE_FILE")
    BEST_ITER=$(jq -r '.best_iteration' "$STATE_FILE")
    TOTAL_ITERS=$(jq -r '.total_iterations' "$STATE_FILE")
    echo "  Total iterations: ${TOTAL_ITERS}"
    echo "  Best pass rate:   ${BEST_RATE}% (iteration ${BEST_ITER})"
fi

echo "  Config: ${AGENT_CONFIG}"
echo "  Artifacts: ${EVOLVE_DIR}/"
echo "=== Done ==="
#!/bin/bash
# evolve_run.sh — Evolution loop for system prompt optimization
#
# Architecture:
#   Fixed base session (warmup) → resume with candidate prompt → evaluate behavior
#
# Flow per iteration:
#   1. Copy base session → resume with --cm-prompt @candidate
#   2. Structural check (--from-message) → behavioral_score
#   3. Knowledge check (LLM judge) → knowledge_score
#   4. combined = 0.25*knowledge + 0.75*behavior
#   5. Accept/reject vs baseline
#   6. Optimizer (LLM with strategy template) → new candidate
#
# All intermediate files saved per-iteration for analysis.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
STRUCTURAL_CHECK="$PROJECT_DIR/structural-check"
TEMPLATES="$SCRIPT_DIR/templates"
AI_BIN="$(which ai)"

log() { echo "[$(date +%H:%M:%S)] $*" >&2; }
die() { echo "[FATAL] $*" >&2; exit 1; }

# --- Args ---
usage() {
    echo "Usage: $0 <prompt_file> [max_iterations] [work_dir]"
    echo ""
    echo "  prompt_file      Path to context_management.md to optimize"
    echo "  max_iterations   Default: 5"
    echo "  work_dir         Default: /tmp/evolve-<timestamp>"
    echo ""
    echo "Requires: base session at /tmp/evolve-base-session/base_session/"
    echo "Create with: cmd/evolve/create_base_session.sh"
    exit 1
}

PROMPT_FILE="${1:-}"
MAX_ITER="${2:-5}"
WORK_DIR="${3:-}"

[[ -z "$PROMPT_FILE" ]] && usage
[[ ! -f "$PROMPT_FILE" ]] && die "Prompt file not found: $PROMPT_FILE"
[[ ! -x "$STRUCTURAL_CHECK" ]] && die "Build structural-check first: go build ./cmd/structural-check"
[[ ! -x "$AI_BIN" ]] && die "ai binary not found in PATH"

# Resolve paths
PROMPT_FILE="$(cd "$(dirname "$PROMPT_FILE")" && pwd)/$(basename "$PROMPT_FILE")"

if [[ -z "$WORK_DIR" ]]; then
    WORK_DIR="/tmp/evolve-$(date +%Y%m%d-%H%M%S)"
fi
mkdir -p "$WORK_DIR"

# --- Base session ---
BASE_SESSION_DIR="/tmp/evolve-base-session/base_session"
BASE_MESSAGES="$BASE_SESSION_DIR/messages.jsonl"

[[ ! -f "$BASE_MESSAGES" ]] && die "Base session not found at $BASE_MESSAGES. Run create_base_session.sh first."

# Count base messages for --from-message offset
BASE_MSG_COUNT=$(wc -l < "$BASE_MESSAGES" | tr -d ' ')
log "Base session: $BASE_MSG_COUNT messages at $BASE_SESSION_DIR"

# --session expects a directory path; LoadSessionLazy appends /messages.jsonl
# We'll create a per-iteration session dir by copying base into it (see run_behavioral_test)

# --- Config ---
KNOWLEDGE_WEIGHT=0.25
BEHAVIORAL_WEIGHT=0.75
TIMEOUT=10m
STRATEGIES=("actionability" "restructure" "simplify")

# --- Template files ---
BEHAVIORAL_TASK_TEMPLATE="$TEMPLATES/behavioral_task_v2.txt"
KNOWLEDGE_RUBRIC="$TEMPLATES/knowledge_rubric.json"

[[ ! -f "$BEHAVIORAL_TASK_TEMPLATE" ]] && die "Missing: $BEHAVIORAL_TASK_TEMPLATE"
[[ ! -f "$KNOWLEDGE_RUBRIC" ]] && die "Missing: $KNOWLEDGE_RUBRIC"

# --- State files ---
BASELINE_PROMPT="$WORK_DIR/baseline_prompt.md"
BASELINE_SCORE_FILE="$WORK_DIR/baseline_score.txt"
CANDIDATE_PROMPT="$WORK_DIR/candidate_prompt.md"
ITERATION_LOG="$WORK_DIR/iterations.tsv"

# --- Init ---
log "=== Evolve Loop ==="
log "Prompt:    $PROMPT_FILE"
log "Work dir:  $WORK_DIR"
log "Max iter:  $MAX_ITER"
log "Weights:   knowledge=$KNOWLEDGE_WEIGHT, behavioral=$BEHAVIORAL_WEIGHT"
log "Base msgs: $BASE_MSG_COUNT"
log ""

# Copy baseline
cp "$PROMPT_FILE" "$BASELINE_PROMPT"
cp "$PROMPT_FILE" "$CANDIDATE_PROMPT"

# TSV header
printf "iter\tstrategy\tknowledge\tbehavioral\tcombined\taccepted\tprompt_bytes\tsession_id\n" > "$ITERATION_LOG"

BASELINE_SCORE=""
FAILED_STRATEGY_COUNT=0

# ==============================
# Functions
# ==============================

run_structural_check() {
    local session_file="$1"
    local trace_file="$2"
    local from_msg="$3"

    if [[ -n "$trace_file" ]] && [[ -f "$trace_file" ]]; then
        "$STRUCTURAL_CHECK" "$session_file" "$trace_file" --from-message "$from_msg" 2>/dev/null
    else
        echo '{"score":0,"checks_passed":0,"checks_total":0,"details":[{"id":"error","passed":false,"message":"no trace file"}]}'
    fi
}

run_knowledge_check() {
    local iter_dir="$1"
    local answers_file="$iter_dir/final_answers.txt"

    if [[ ! -f "$answers_file" ]] || [[ ! -s "$answers_file" ]]; then
        echo "0.0"
        return
    fi

    # Build judge task
    local judge_task="$iter_dir/knowledge_judge_task.txt"
    local judge_result="$iter_dir/knowledge_judge_result.txt"
    rm -f "$judge_result"

    {
        echo "Score the following answers against the rubric."
        echo ""
        echo "=== Answers ==="
        cat "$answers_file"
        echo ""
        echo "=== Rubric ==="
        cat "$KNOWLEDGE_RUBRIC"
        echo ""
        echo "Write ONLY this line to $judge_result:"
        echo "SCORE:0.0-1.0"
        echo ""
        echo "Where the score is the average across all questions (0.0-1.0 each)."
    } > "$judge_task"

    # Run judge via headless (needs write tool)
    timeout 3m "$AI_BIN" \
        --mode headless \
        --system-prompt "You are a grader. Score answers precisely. Write ONLY: SCORE:0.0-1.0 to the specified file." \
        --tools "write" \
        --max-turns 5 \
        --timeout 3m \
        "$(cat "$judge_task")" \
        > "$iter_dir/knowledge_judge_output.txt" 2>&1 || true

    # Extract score
    if [[ -f "$judge_result" ]]; then
        grep -o 'SCORE:[0-9.]*' "$judge_result" | head -1 | sed 's/SCORE://'
    else
        echo "0.0"
    fi
}

run_optimizer() {
    local iter="$1"
    local strategy="$2"
    local candidate="$3"
    local structural_json="$4"
    local knowledge_score="$5"
    local iter_dir="$WORK_DIR/iter${iter}"

    local strategy_file="$TEMPLATES/strategies/${strategy}.md"
    if [[ ! -f "$strategy_file" ]]; then
        log "  WARNING: Strategy $strategy not found at $strategy_file"
        echo ""
        return
    fi

    local opt_task="$iter_dir/optimizer_task.txt"
    local opt_result="$iter_dir/optimizer_prompt.md"
    rm -f "$opt_result"

    {
        cat "$strategy_file"
        echo ""
        echo "=== Current Prompt ==="
        cat "$candidate"
        echo ""
        echo "=== Test Results ==="

        # Include behavioral check failures
        if [[ -f "$structural_json" ]]; then
            echo "Behavioral checks:"
            python3 -c "
import sys, json
with open('$structural_json') as f:
    data = json.load(f)
for d in data.get('details', []):
    icon = 'PASS' if d['passed'] else 'FAIL'
    print(f'  [{icon}] {d[\"id\"]}: {d[\"message\"]}')
print(f'Overall: {data[\"score\"]:.3f} ({data[\"checks_passed\"]}/{data[\"checks_total\"]})')
" 2>/dev/null || echo "  (could not parse structural results)"
        fi

        echo "Knowledge score: ${knowledge_score}"
        echo ""
        echo "Write the COMPLETE improved prompt to: $opt_result"
    } > "$opt_task"

    # Run optimizer
    timeout 5m "$AI_BIN" \
        --mode headless \
        --system-prompt "You are a prompt optimization specialist. Write the COMPLETE improved prompt to the specified file. Only the prompt, no commentary." \
        --tools "write,read" \
        --max-turns 10 \
        --timeout 5m \
        "$(cat "$opt_task")" \
        > "$iter_dir/optimizer_output.txt" 2>&1 || true

    if [[ -f "$opt_result" ]] && [[ -s "$opt_result" ]]; then
        echo "$opt_result"
    else
        echo ""
    fi
}

# ==============================
# Main Loop
# ==============================

for ITER in $(seq 1 "$MAX_ITER"); do
    log ""
    log "========== Iteration $ITER/$MAX_ITER =========="

    ITER_DIR="$WORK_DIR/iter${ITER}"
    mkdir -p "$ITER_DIR"

    # Clean output files from previous runs
    rm -f "$ITER_DIR"/step*.txt "$ITER_DIR"/final_answers.txt

    # --- Step 1: Behavioral test (resume base session) ---
    log "Step 1: Behavioral test (resume base session)..."

    # Create a temp session directory for this iteration.
    # --session expects a directory; LoadSessionLazy reads <dir>/messages.jsonl.
    # sessionsDir = filepath.Dir(sessionDir), so we need:
    #   parent_dir/session_name/messages.jsonl
    session_parent="$ITER_DIR/sessions"
    session_name="warmup"
    session_dir="$session_parent/$session_name"
    mkdir -p "$session_dir"
    cp "$BASE_MESSAGES" "$session_dir/messages.jsonl"
    log "  Copied base session to $session_dir"

    # Build task with work_dir substituted
    local_task="$ITER_DIR/task.txt"
    sed "s|{{WORK_DIR}}|$ITER_DIR|g" "$BEHAVIORAL_TASK_TEMPLATE" > "$local_task"

    # Run headless, resuming from copied base session
    log "  Resuming base session ($BASE_MSG_COUNT messages)..."
    local_headless_out="$ITER_DIR/headless_output.txt"

    cd "$ITER_DIR"
    timeout "${TIMEOUT%m}m" "$AI_BIN" \
        --mode headless \
        --session "$session_dir" \
        --cm-prompt "@$CANDIDATE_PROMPT" \
        --timeout "$TIMEOUT" \
        --max-turns 30 \
        "$(cat "$local_task")" \
        > "$local_headless_out" 2>&1 || true
    cd "$WORK_DIR"

    # Extract session info
    SESSION_ID=""
    SESSION_FILE=""

    if grep -q "Session ID:" "$local_headless_out" 2>/dev/null; then
        SESSION_ID=$(grep "Session ID:" "$local_headless_out" | head -1 | sed 's/.*Session ID: //' | tr -d '[:space:]')
        SESSION_FILE=$(grep "Session file:" "$local_headless_out" | head -1 | sed 's/.*Session file: //' | tr -d '[:space:]')
    fi

    if [[ -z "$SESSION_ID" ]]; then
        log "  FAILED: Could not extract session ID. Skipping iteration."
        echo -e "${ITER}\terr\t0\t0\t0\tno\t0\t" >> "$ITERATION_LOG"
        continue
    fi

    log "  Session: $SESSION_ID"

    # Find the session file for analysis.
    # headless writes to sessionsDir/sessionID/messages.jsonl
    # sessionsDir = filepath.Dir(session_dir) = session_parent
    # sessionID = sessionIDFromDirPath(session_dir) = "warmup" 
    # So final path = session_parent/warmup/messages.jsonl (same dir, updated in-place)
    if [[ -f "$session_dir/messages.jsonl" ]]; then
        cp "$session_dir/messages.jsonl" "$ITER_DIR/messages.jsonl"
        TOTAL_MSGS=$(wc -l < "$ITER_DIR/messages.jsonl" | tr -d ' ')
        SESSION_FILE="$ITER_DIR/messages.jsonl"
    elif [[ -n "$SESSION_FILE" ]] && [[ -f "$SESSION_FILE" ]]; then
        TOTAL_MSGS=$(wc -l < "$SESSION_FILE" | tr -d ' ')
        cp "$SESSION_FILE" "$ITER_DIR/messages.jsonl"
    else
        log "  WARNING: Cannot find session file"
        TOTAL_MSGS=$BASE_MSG_COUNT
    fi

    log "  Session file: $(du -h "$ITER_DIR/messages.jsonl" | cut -f1) ($TOTAL_MSGS messages, $BASE_MSG_COUNT warmup)"

    # Find trace
    TRACE_FILE=$(ls -t "$HOME/.ai/traces/"*"*$SESSION_ID"* 2>/dev/null | head -1 || true)
    if [[ -n "$TRACE_FILE" ]] && [[ -f "$TRACE_FILE" ]]; then
        cp "$TRACE_FILE" "$ITER_DIR/trace.json"
        TRACE_FILE="$ITER_DIR/trace.json"
    else
        # Create empty trace
        echo '{"traceEvents":[]}' > "$ITER_DIR/empty_trace.json"
        TRACE_FILE="$ITER_DIR/empty_trace.json"
    fi

    # Verify output files exist (from behavioral task)
    OUTPUT_FILES=("step2_analysis.txt" "step3_comparison.txt" "final_answers.txt")
    for f in "${OUTPUT_FILES[@]}"; do
        if [[ -f "$ITER_DIR/$f" ]]; then
            log "  ✅ $f exists"
        else
            log "  ❌ $f missing"
        fi
    done

    # --- Step 2: Structural check ---
    # Prefer pre-compact backup (full unmodified messages) over compacted messages.jsonl.
    # Backup is created by SaveMessages when compact shrinks the session.
    PRE_COMPACT=""
    for f in "$session_dir"/llm-context/detail/pre-compact-*.jsonl; do
        [[ -f "$f" ]] || continue
        PRE_COMPACT="$f"
    done
    if [[ -n "$PRE_COMPACT" ]]; then
        cp "$PRE_COMPACT" "$ITER_DIR/pre_compact_messages.jsonl"
        log "Step 2: Structural check (using pre-compact backup, from-message=$BASE_MSG_COUNT)..."
        STRUCTURAL_RESULT=$(run_structural_check "$ITER_DIR/pre_compact_messages.jsonl" "$TRACE_FILE" "$BASE_MSG_COUNT")
    else
        log "Step 2: Structural check (from-message=$BASE_MSG_COUNT)..."
        STRUCTURAL_RESULT=$(run_structural_check "$ITER_DIR/messages.jsonl" "$TRACE_FILE" "$BASE_MSG_COUNT")
    fi
    echo "$STRUCTURAL_RESULT" > "$ITER_DIR/structural_result.json"

    BEHAVIORAL_SCORE=$(echo "$STRUCTURAL_RESULT" | python3 -c "import sys,json; print(json.load(sys.stdin)['score'])" 2>/dev/null || echo "0.0")
    BEH_PASSED=$(echo "$STRUCTURAL_RESULT" | python3 -c "import sys,json; d=json.load(sys.stdin); print(f\"{d['checks_passed']}/{d['checks_total']}\")" 2>/dev/null || echo "0/0")
    log "  Behavioral: $BEHAVIORAL_SCORE ($BEH_PASSED checks passed)"

    # Log each check detail
    echo "$STRUCTURAL_RESULT" | python3 -c "
import sys, json
for d in json.load(sys.stdin).get('details', []):
    icon = '✅' if d['passed'] else '❌'
    print(f'    {icon} {d[\"id\"]}: {d[\"message\"]}')
" 2>/dev/null | while IFS= read -r line; do log "$line"; done

    # --- Step 3: Knowledge check ---
    log "Step 3: Knowledge check..."
    KNOWLEDGE_SCORE=$(run_knowledge_check "$ITER_DIR")
    log "  Knowledge: $KNOWLEDGE_SCORE"

    # --- Step 4: Combined score ---
    COMBINED=$(python3 -c "
k = float('${KNOWLEDGE_SCORE:-0}')
b = float('${BEHAVIORAL_SCORE:-0}')
print(f'{k * $KNOWLEDGE_WEIGHT + b * $BEHAVIORAL_WEIGHT:.3f}')
")
    log "  Combined: $COMBINED (k=$KNOWLEDGE_SCORE * $KNOWLEDGE_WEIGHT + b=$BEHAVIORAL_SCORE * $BEHAVIORAL_WEIGHT)"

    # --- Step 5: Accept/Reject ---
    ACCEPTED=false
    PROMPT_BYTES=$(wc -c < "$CANDIDATE_PROMPT" | tr -d ' ')

    if [[ -z "$BASELINE_SCORE" ]]; then
        BASELINE_SCORE="$COMBINED"
        echo "$BASELINE_SCORE" > "$BASELINE_SCORE_FILE"
        log "  BASELINE established: $BASELINE_SCORE"
        ACCEPTED=true
    else
        BETTER=$(python3 -c "
baseline = float('$BASELINE_SCORE')
current = float('$COMBINED')
print('yes' if current > baseline else 'no')
")
        if [[ "$BETTER" == "yes" ]]; then
            log "  ✅ ACCEPTED: $COMBINED > baseline $BASELINE_SCORE"
            BASELINE_SCORE="$COMBINED"
            echo "$BASELINE_SCORE" > "$BASELINE_SCORE_FILE"
            cp "$CANDIDATE_PROMPT" "$BASELINE_PROMPT"
            ACCEPTED=true
            FAILED_STRATEGY_COUNT=0
        else
            log "  ❌ REJECTED: $COMBINED <= baseline $BASELINE_SCORE"
            cp "$BASELINE_PROMPT" "$CANDIDATE_PROMPT"
            FAILED_STRATEGY_COUNT=$((FAILED_STRATEGY_COUNT + 1))
        fi
    fi

    # --- Step 6: Log ---
    echo -e "${ITER}\t${STRATEGY_NAME:-initial}\t${KNOWLEDGE_SCORE}\t${BEHAVIORAL_SCORE}\t${COMBINED}\t${ACCEPTED}\t${PROMPT_BYTES}\t${SESSION_ID}" >> "$ITERATION_LOG"

    # Check convergence
    if python3 -c "exit(0 if float('$COMBINED') >= 0.90 else 1)" 2>/dev/null; then
        log ""
        log "CONVERGED at iteration $ITER (score=$COMBINED)"
        break
    fi

    # --- Step 7: Optimize (skip last iteration) ---
    if (( ITER < MAX_ITER )); then
        if (( FAILED_STRATEGY_COUNT >= 2 )); then
            STRATEGY_IDX=$(( (ITER + FAILED_STRATEGY_COUNT) % ${#STRATEGIES[@]} ))
            FAILED_STRATEGY_COUNT=0
            log "  Switching strategy after consecutive failures"
        else
            STRATEGY_IDX=$(( (ITER - 1) % ${#STRATEGIES[@]} ))
        fi
        STRATEGY_NAME="${STRATEGIES[$STRATEGY_IDX]}"
        log "Step 4: Optimizer (strategy=$STRATEGY_NAME)..."

        OPT_RESULT=$(run_optimizer "$ITER" "$STRATEGY_NAME" "$CANDIDATE_PROMPT" "$ITER_DIR/structural_result.json" "$KNOWLEDGE_SCORE")

        if [[ -n "$OPT_RESULT" ]] && [[ -f "$OPT_RESULT" ]]; then
            cp "$OPT_RESULT" "$CANDIDATE_PROMPT"
            NEW_BYTES=$(wc -c < "$CANDIDATE_PROMPT" | tr -d ' ')
            log "  New candidate: $NEW_BYTES bytes"
        else
            log "  WARNING: Optimizer failed, keeping current candidate"
        fi
    fi
done

# --- Summary ---
log ""
log "========== Summary =========="
log "Iterations: $(( ITER <= MAX_ITER ? ITER : MAX_ITER ))"
log "Baseline score: $BASELINE_SCORE"
log "Best prompt: $BASELINE_PROMPT"
log "Full log: $ITERATION_LOG"
log ""
log "Per-iteration files:"
for d in "$WORK_DIR"/iter*/; do
    [[ -d "$d" ]] || continue
    n=$(basename "$d")
    log "  $n/: $(ls "$d" | tr '\n' ' ')"
done
log ""
log "TSV:"
cat "$ITERATION_LOG"

log ""
if python3 -c "exit(0 if float('${BASELINE_SCORE:-0}') > 0.3 else 1)" 2>/dev/null; then
    log "Evolved prompt beats original threshold."
    log "To apply: cp $BASELINE_PROMPT $PROMPT_FILE"
else
    log "Evolved prompt did not significantly improve over original."
fi
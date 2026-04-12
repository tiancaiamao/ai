#!/bin/bash
# pair.sh — Worker-Judge loop
#
# Usage: pair.sh <worker-prompt> <judge-prompt> <input-file> [max-rounds]
#
# In mock mode (AG_MOCK=1): prompt args are treated as mock-script paths.
# In real mode: prompt args are --system values.
#
# Environment:
#   AG_BIN    — path to ag binary (default: ag)
#   AG_MOCK   — set to "1" to use mock agents
#
set -euo pipefail

AG_BIN="${AG_BIN:-ag}"
WORKER_PROMPT="$1"
JUDGE_PROMPT="$2"
INPUT_FILE="$3"
MAX_ROUNDS="${4:-3}"
MOCK="${AG_MOCK:-}"

# Build spawn flags based on mock mode
spawn_flags() {
  local script="$1"
  if [ -n "$MOCK" ]; then
    echo "--mock --mock-script $script"
  else
    echo "--system $script"
  fi
}

echo "[pair] Starting worker-judge loop (max $MAX_ROUNDS rounds, mock=${MOCK:-none})"

FEEDBACK_FILE=""

for round in $(seq 1 "$MAX_ROUNDS"); do
  # Unique IDs per round to avoid "agent already exists"
  WORKER_ID="pair-w-$$-r${round}"
  JUDGE_ID="pair-j-$$-r${round}"

  echo "[pair] === Round $round ==="

  # --- Spawn worker ---
  WORKER_FLAGS=$(spawn_flags "$WORKER_PROMPT")
  if [ "$round" -eq 1 ]; then
    eval $AG_BIN spawn --id "\"$WORKER_ID\"" $WORKER_FLAGS --input "\"$INPUT_FILE\"" --timeout 10m
  else
    eval $AG_BIN spawn --id "\"$WORKER_ID\"" $WORKER_FLAGS --input "\"$FEEDBACK_FILE\"" --timeout 10m
  fi

  # --- Wait for worker ---
  echo "[pair] Waiting for worker ($WORKER_ID)..."
  if ! $AG_BIN wait "$WORKER_ID" --timeout 60; then
    echo "[pair] Worker failed in round $round"
    $AG_BIN rm "$WORKER_ID" 2>/dev/null || true
    continue
  fi

  # --- Get worker output ---
  WORKER_OUTPUT=$(mktemp)
  $AG_BIN output "$WORKER_ID" > "$WORKER_OUTPUT"
  echo "[pair] Worker output: $(wc -l < "$WORKER_OUTPUT" | tr -d ' ') lines"

  # Clean up worker agent
  $AG_BIN rm "$WORKER_ID" 2>/dev/null || true

  # --- Spawn judge ---
  JUDGE_FLAGS=$(spawn_flags "$JUDGE_PROMPT")
  eval $AG_BIN spawn --id "\"$JUDGE_ID\"" $JUDGE_FLAGS --input "\"$WORKER_OUTPUT\"" --timeout 5m

  # --- Wait for judge ---
  echo "[pair] Waiting for judge ($JUDGE_ID)..."
  if ! $AG_BIN wait "$JUDGE_ID" --timeout 30; then
    echo "[pair] Judge failed in round $round"
    $AG_BIN rm "$JUDGE_ID" 2>/dev/null || true
    rm -f "$WORKER_OUTPUT"
    continue
  fi

  # --- Get judge verdict ---
  JUDGE_OUTPUT=$(mktemp)
  $AG_BIN output "$JUDGE_ID" > "$JUDGE_OUTPUT"
  echo "[pair] Judge verdict:"
  cat "$JUDGE_OUTPUT"

  # Clean up judge agent
  $AG_BIN rm "$JUDGE_ID" 2>/dev/null || true

  # --- Check verdict ---
  if grep -qi "APPROVED\|PASS\|ACCEPT" "$JUDGE_OUTPUT"; then
    echo "[pair] ✅ Approved in round $round"
    cat "$WORKER_OUTPUT"
    rm -f "$WORKER_OUTPUT" "$JUDGE_OUTPUT"
    [ -n "$FEEDBACK_FILE" ] && rm -f "$FEEDBACK_FILE"
    exit 0
  fi

  # --- Prepare feedback for next round ---
  FEEDBACK_FILE=$(mktemp)
  {
    echo "# Previous attempt (round $round)"
    echo ""
    cat "$WORKER_OUTPUT"
    echo ""
    echo "# Judge feedback"
    echo ""
    cat "$JUDGE_OUTPUT"
    echo ""
    echo "# Instructions"
    echo "Address the judge's feedback above and produce an improved version."
  } > "$FEEDBACK_FILE"

  rm -f "$WORKER_OUTPUT" "$JUDGE_OUTPUT"
  echo "[pair] ❌ Not approved, starting round $((round + 1))"
done

echo "[pair] ❌ Max rounds ($MAX_ROUNDS) reached without approval"
[ -n "$FEEDBACK_FILE" ] && rm -f "$FEEDBACK_FILE"
exit 1
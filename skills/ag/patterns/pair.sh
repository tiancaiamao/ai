#!/bin/bash
# pair.sh — Worker-Judge loop
#
# Usage: pair.sh <worker-system-prompt-file> <judge-system-prompt-file> <input-file> [max-rounds]
#
# Worker and judge prompt args are file paths. Their contents are read and
# passed as --system to ag agent spawn.
#
# The worker agent is kept alive across rounds so it retains context from
# previous iterations. Judge feedback is sent as a follow-up prompt via
# `ag agent prompt`. The judge is freshly spawned each round to maintain
# independence.
#
# Environment:
#   AG_BINARY — path to ag binary (defaults to "ag")
#
set -euo pipefail

AG_BINARY="${AG_BINARY:-${AG_BIN:-ag}}"

WORKER_PROMPT_FILE="$1"
JUDGE_PROMPT_FILE="$2"
INPUT_FILE="$3"
MAX_ROUNDS="${4:-3}"

WORKER_ID="pair-w-$$"
WORKER_RUNNING=false

cleanup() {
  if [ "$WORKER_RUNNING" = true ]; then
    echo "[pair] Cleaning up worker agent ($WORKER_ID)..."
    $AG_BINARY agent rm "$WORKER_ID" 2>/dev/null || true
    WORKER_RUNNING=false
  fi
}
trap cleanup EXIT

# --system @file reads the file content directly.
# Prompt file args are paths prefixed with @.

echo "[pair] Starting worker-judge loop (max $MAX_ROUNDS rounds)"
echo "[pair] Worker prompt: $WORKER_PROMPT_FILE ($(wc -l < "$WORKER_PROMPT_FILE" | tr -d ' ') lines)"
echo "[pair] Judge prompt:  $JUDGE_PROMPT_FILE ($(wc -l < "$JUDGE_PROMPT_FILE" | tr -d ' ') lines)"

for round in $(seq 1 "$MAX_ROUNDS"); do
  JUDGE_ID="pair-j-$$-r${round}"

  echo "[pair] === Round $round ==="

  if [ "$round" -eq 1 ]; then
    # --- Spawn worker (once, at start of round 1) ---
    $AG_BINARY agent spawn "$WORKER_ID" \
      --system "@$WORKER_PROMPT_FILE" \
      --input "$(cat "$INPUT_FILE")"
    WORKER_RUNNING=true
  else
    # --- Resume worker with judge feedback (not respawn) ---
    echo "[pair] Sending judge feedback to existing worker ($WORKER_ID)..."
    $AG_BINARY agent prompt "$WORKER_ID" "$(cat "$FEEDBACK_FILE")"
  fi

  # --- Wait for worker ---
  echo "[pair] Waiting for worker ($WORKER_ID)..."
  if ! $AG_BINARY agent wait "$WORKER_ID" --timeout 120; then
    echo "[pair] Worker failed in round $round"
    continue
  fi

  # --- Get worker output ---
  WORKER_OUTPUT=$(mktemp)
  $AG_BINARY agent output "$WORKER_ID" > "$WORKER_OUTPUT"
  echo "[pair] Worker output: $(wc -l < "$WORKER_OUTPUT" | tr -d ' ') lines"

  # --- Spawn judge (fresh each round for independence) ---
  JUDGE_INPUT=$(cat "$WORKER_OUTPUT")
  $AG_BINARY agent spawn "$JUDGE_ID" \
    --system "@$JUDGE_PROMPT_FILE" \
    --input "$JUDGE_INPUT"

  # --- Wait for judge ---
  echo "[pair] Waiting for judge ($JUDGE_ID)..."
  if ! $AG_BINARY agent wait "$JUDGE_ID" --timeout 60; then
    echo "[pair] Judge failed in round $round"
    $AG_BINARY agent rm "$JUDGE_ID" 2>/dev/null || true
    rm -f "$WORKER_OUTPUT"
    continue
  fi

  # --- Get judge verdict ---
  JUDGE_OUTPUT=$(mktemp)
  $AG_BINARY agent output "$JUDGE_ID" > "$JUDGE_OUTPUT"
  echo "[pair] Judge verdict:"
  cat "$JUDGE_OUTPUT"

  # Clean up judge agent
  $AG_BINARY agent rm "$JUDGE_ID" 2>/dev/null || true

  # --- Check verdict ---
  if grep -qi "APPROVED\|PASS\|ACCEPT" "$JUDGE_OUTPUT"; then
    echo "[pair] ✅ Approved in round $round"
    cat "$WORKER_OUTPUT"
    rm -f "$WORKER_OUTPUT" "$JUDGE_OUTPUT"
    [ -n "${FEEDBACK_FILE:-}" ] && rm -f "$FEEDBACK_FILE"
    exit 0
  fi

  # --- Prepare feedback for next round ---
  FEEDBACK_FILE=$(mktemp)
  {
    echo "# Judge feedback from round $round"
    echo ""
    cat "$JUDGE_OUTPUT"
    echo ""
    echo "# Instructions"
    echo "Address the judge's feedback above and produce an improved version of your previous attempt."
  } > "$FEEDBACK_FILE"

  rm -f "$WORKER_OUTPUT" "$JUDGE_OUTPUT"
  echo "[pair] ❌ Not approved, starting round $((round + 1))"
done

echo "[pair] ❌ Max rounds ($MAX_ROUNDS) reached without approval"
[ -n "${FEEDBACK_FILE:-}" ] && rm -f "$FEEDBACK_FILE"
exit 1
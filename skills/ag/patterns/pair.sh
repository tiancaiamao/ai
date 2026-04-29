#!/bin/bash
# pair.sh — Worker-Judge loop
#
# Usage: pair.sh <worker-prompt> <judge-prompt> <input-file> [max-rounds]
#
# In mock mode (AG_MOCK=1): prompt args are treated as mock-script paths.
# In real mode: prompt args are --system values.
#
# Environment:
#   AG_MOCK   — set to "1" to use mock agents
#   AG_BINARY — path to ag binary (defaults to "ag")
#
set -euo pipefail

AG_BINARY="${AG_BINARY:-${AG_BIN:-ag}}"

WORKER_PROMPT="$1"
JUDGE_PROMPT="$2"
INPUT_FILE="$3"
MAX_ROUNDS="${4:-3}"
MOCK="${AG_MOCK:-}"

echo "[pair] Starting worker-judge loop (max $MAX_ROUNDS rounds, mock=${MOCK:-none})"

FEEDBACK_FILE=""

for round in $(seq 1 "$MAX_ROUNDS"); do
  # Unique IDs per round to avoid "agent already exists"
  WORKER_ID="pair-w-$$-r${round}"
  JUDGE_ID="pair-j-$$-r${round}"

  echo "[pair] === Round $round ==="

    # --- Spawn worker ---
  CURRENT_INPUT="$INPUT_FILE"
  if [ "$round" -gt 1 ]; then
    if [ -z "$FEEDBACK_FILE" ]; then
      echo "[pair] Error: no feedback available for round $round"
      break
    fi
    CURRENT_INPUT="$FEEDBACK_FILE"
  fi

                SPAWN_ARGS=("$WORKER_ID" --input "$CURRENT_INPUT")
  if [ -n "$MOCK" ]; then
    # In mock mode, use bash backend
    SPAWN_ARGS+=(--backend bash)
  else
    SPAWN_ARGS+=(--system "$WORKER_PROMPT")
    fi
  $AG_BINARY agent spawn "${SPAWN_ARGS[@]}"

    # --- Wait for worker ---
  echo "[pair] Waiting for worker ($WORKER_ID)..."
  if ! $AG_BINARY agent wait "$WORKER_ID" --timeout 10; then
    echo "[pair] Worker failed in round $round"
    $AG_BINARY agent rm "$WORKER_ID" 2>/dev/null || true
    continue
  fi

  # --- Get worker output ---
    WORKER_OUTPUT=$(mktemp)
  $AG_BINARY agent output "$WORKER_ID" > "$WORKER_OUTPUT"
  echo "[pair] Worker output: $(wc -l < "$WORKER_OUTPUT" | tr -d ' ') lines"

    # Clean up worker agent
  $AG_BINARY agent rm "$WORKER_ID" 2>/dev/null || true

                                # --- Spawn judge ---
  JUDGE_ARGS=("$JUDGE_ID" --input "$WORKER_OUTPUT")
  if [ -n "$MOCK" ]; then
    # In mock mode, use judge backend
    JUDGE_ARGS+=(--backend judge)
  else
    JUDGE_ARGS+=(--system "$JUDGE_PROMPT")
  fi
  $AG_BINARY agent spawn "${JUDGE_ARGS[@]}"

                # --- Wait for judge ---
  echo "[pair] Waiting for judge ($JUDGE_ID)..."
  if ! $AG_BINARY agent wait "$JUDGE_ID" --timeout 5; then
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
    [ -n "$FEEDBACK_FILE" ] && rm -f "$FEEDBACK_FILE"
    exit 0
  fi

  # --- Prepare feedback for next round ---
  FEEDBACK_FILE=$(mktemp)
  {
    echo "# Original task (from round 1)"
    echo ""
    cat "$INPUT_FILE"
    echo ""
    echo "# Previous attempt (round $round)"
    echo ""
    cat "$WORKER_OUTPUT"
    echo ""
    echo "# Judge feedback"
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
[ -n "$FEEDBACK_FILE" ] && rm -f "$FEEDBACK_FILE"
exit 1

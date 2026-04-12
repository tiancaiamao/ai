#!/bin/bash
# pipeline.sh — Run stages sequentially, each stage's output feeds the next
#
# Usage: pipeline.sh <input-file> <stage1-prompt> [stage2-prompt] ...
#
# Environment:
#   AG_MOCK   — set to "1" for mock mode
#
set -euo pipefail

MOCK="${AG_MOCK:-}"
INPUT_FILE="$1"
shift

STAGES=("$@")
STAGE_COUNT=${#STAGES[@]}

if [ "$STAGE_COUNT" -eq 0 ]; then
  echo "Usage: pipeline.sh <input-file> <stage1-prompt> [stage2-prompt] ..."
  exit 1
fi

# Track temp files for cleanup on failure
TEMP_FILES=()
cleanup_temps() {
  for f in "${TEMP_FILES[@]}"; do
    rm -f "$f"
  done
}
trap cleanup_temps EXIT

echo "[pipeline] Running $STAGE_COUNT stages (mock=$MOCK)"

PREV_OUTPUT="$INPUT_FILE"

for i in "${!STAGES[@]}"; do
  STAGE_NUM=$((i + 1))
  STAGE_PROMPT="${STAGES[$i]}"
  STAGE_ID="pipeline-$$-stage-$STAGE_NUM"

  echo "[pipeline] === Stage $STAGE_NUM/$STAGE_COUNT ==="

  if [ -n "$MOCK" ]; then
    ag spawn --id "$STAGE_ID" --mock --mock-script "$STAGE_PROMPT" --input "$PREV_OUTPUT" --timeout 1m
  else
    ag spawn --id "$STAGE_ID" --system "$STAGE_PROMPT" --input "$PREV_OUTPUT" --timeout 10m
  fi

  if ! ag wait "$STAGE_ID" --timeout 60; then
    echo "[pipeline] ❌ Stage $STAGE_NUM failed"
    exit 1
  fi

  PREV_OUTPUT=$(mktemp)
  TEMP_FILES+=("$PREV_OUTPUT")
  ag output "$STAGE_ID" > "$PREV_OUTPUT"

  LINES=$(wc -l < "$PREV_OUTPUT" | tr -d ' ')
  echo "[pipeline] Stage $STAGE_NUM done ($LINES lines)"
done

echo "[pipeline] ✅ Pipeline complete ($STAGE_COUNT stages)"
cat "$PREV_OUTPUT"

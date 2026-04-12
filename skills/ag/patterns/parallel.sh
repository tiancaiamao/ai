#!/bin/bash
# parallel.sh — Spawn N agents in parallel, collect results
#
# Usage: parallel.sh <count> <system-prompt> <input-topic> [output-dir]
#
# Environment:
#   AG_MOCK — set to "1" for mock mode
#
set -euo pipefail

MOCK="${AG_MOCK:-}"
COUNT="$1"
SYSTEM_PROMPT="$2"
INPUT_TEXT="$3"
OUTPUT_DIR="${4:-}"

CLEANUP_OUTPUT=false
if [ -z "$OUTPUT_DIR" ]; then
  OUTPUT_DIR=$(mktemp -d)
  CLEANUP_OUTPUT=true
fi
mkdir -p "$OUTPUT_DIR"

echo "[parallel] Spawning $COUNT agents (mock=$MOCK)..."

PIDS=()
AGENT_IDS=()
TEMP_INPUTS=()

for i in $(seq 0 $((COUNT - 1))); do
  ID="parallel-$$-$i"
  AGENT_IDS+=("$ID")

  AGENT_INPUT=$(mktemp)
  cat > "$AGENT_INPUT" <<EOF
Parallel task index: $i of $((COUNT - 1))
Topic: $INPUT_TEXT

You are agent #$i working on this task in parallel with $((COUNT - 1)) other agents.
Each agent may approach the task from a different angle or cover a different aspect.
Focus on your unique perspective as agent #$i.
EOF
  TEMP_INPUTS+=("$AGENT_INPUT")

  if [ -n "$MOCK" ]; then
    ag spawn --id "$ID" --mock --mock-script "$SYSTEM_PROMPT" --input "$AGENT_INPUT" --timeout 1m &
  else
    ag spawn --id "$ID" --system "$SYSTEM_PROMPT" --input "$AGENT_INPUT" --timeout 10m &
  fi
  PIDS+=($!)
done

# Wait for all spawns to complete before cleaning up input files
for pid in "${PIDS[@]}"; do
  wait "$pid" 2>/dev/null || true
done

# Now safe to remove temp input files
for tmp in "${TEMP_INPUTS[@]}"; do
  rm -f "$tmp"
done

echo "[parallel] All agents spawned. Waiting for completion..."

FAILED=0
for i in "${!AGENT_IDS[@]}"; do
  ID="${AGENT_IDS[$i]}"
  if ag wait "$ID" --timeout 60; then
    ag output "$ID" > "$OUTPUT_DIR/agent-$i.md"
    echo "[parallel] Agent $i done"
  else
    echo "[parallel] Agent $i failed"
    echo "FAILED" > "$OUTPUT_DIR/agent-$i.md"
    FAILED=$((FAILED + 1))
  fi
done

echo "[parallel] $((COUNT - FAILED))/$COUNT agents succeeded"

if [ "$FAILED" -eq "$COUNT" ]; then
  echo "[parallel] ❌ All agents failed"
  exit 1
fi

echo ""
echo "[parallel] === Results ==="
for i in $(seq 0 $((COUNT - 1))); do
  echo "--- Agent $i ---"
  cat "$OUTPUT_DIR/agent-$i.md"
  echo ""
done

if [ "$CLEANUP_OUTPUT" = true ]; then
  rm -rf "$OUTPUT_DIR"
else
  echo "[parallel] ✅ Output directory: $OUTPUT_DIR"
fi

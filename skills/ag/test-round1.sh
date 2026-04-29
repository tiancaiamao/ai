#!/bin/bash
# Simple test for the first round of pair.sh
set -euo pipefail

WORKSPACE=$(mktemp -d)
cd "$WORKSPACE"
cp /Users/genius/.ai/skills/ag/backends.yaml ./backends.yaml
echo "write a hello world function" > input.txt

echo "[debug] Testing round 1 of pair.sh..."

WORKER_ID="pair-w-$$-r1"
INPUT_FILE="input.txt"
MOCK="1"

echo "[debug] Files in directory:"
ls -la

echo "[debug] Spawning worker..."
SPAWN_ARGS=("$WORKER_ID" --input "$INPUT_FILE")
if [ -n "$MOCK" ]; then
    SPAWN_ARGS+=(--backend bash)
fi
echo "[debug] Command: /Users/genius/.ai/skills/ag/ag agent spawn ${SPAWN_ARGS[*]}"
/Users/genius/.ai/skills/ag/ag agent spawn "${SPAWN_ARGS[@]}"

echo "[debug] Waiting for worker..."
if /Users/genius/.ai/skills/ag/ag agent wait "$WORKER_ID" --timeout 10; then
    echo "[debug] Worker completed successfully"
    WORKER_OUTPUT=$(mktemp)
    /Users/genius/.ai/skills/ag/ag agent output "$WORKER_ID" > "$WORKER_OUTPUT"
    echo "[debug] Worker output:"
    cat "$WORKER_OUTPUT"
    rm -f "$WORKER_OUTPUT"
    echo "[debug] Round 1 test PASSED"
    exit 0
else
    echo "[debug] Worker FAILED in round 1"
    exit 1
fi
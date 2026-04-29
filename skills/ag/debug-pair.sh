#!/bin/bash
# Debug script to test pair.sh logic
set -euo pipefail

WORKSPACE=$(mktemp -d)
cd "$WORKSPACE"
cp /Users/genius/.ai/skills/ag/backends.yaml ./backends.yaml
echo "write a hello world function" > input.txt

echo "[debug] Working directory: $(pwd)"
echo "[debug] Files in directory:"
ls -la

echo "[debug] Testing bash backend directly..."
WORKER_ID="debug-worker-$$"
echo "[debug] Spawning worker: ag agent spawn $WORKER_ID --backend bash --input input.txt"
/Users/genius/.ai/skills/ag/ag agent spawn "$WORKER_ID" --backend bash --input input.txt

echo "[debug] Waiting for worker: ag agent wait $WORKER_ID --timeout 10"
if /Users/genius/.ai/skills/ag/ag agent wait "$WORKER_ID" --timeout 10; then
    echo "[debug] Worker completed successfully"
    echo "[debug] Worker output:"
    /Users/genius/.ai/skills/ag/ag agent output "$WORKER_ID"
else
    echo "[debug] Worker failed"
    exit 1
fi

echo "[debug] Test completed successfully"
rm -rf "$WORKSPACE"
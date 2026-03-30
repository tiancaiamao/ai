#!/bin/bash
# create_base_session.sh — Build a fixed base session for evolve loop
#
# This creates a "warmup" session that simulates a real agent session
# with significant context. The evolve loop resumes from this session
# to test context management under realistic conditions.
#
# Usage: ./create_base_session.sh [output_dir]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
TEMPLATES="$SCRIPT_DIR/templates"
AI_BIN="$(which ai)"

OUTPUT_DIR="${1:-/tmp/evolve-base-session}"
mkdir -p "$OUTPUT_DIR"

log() { echo "[$(date +%H:%M:%S)] $*" >&2; }

# Build warmup task
WARMUP_TASK="$OUTPUT_DIR/warmup_task.txt"
sed "s|{{WORK_DIR}}|$OUTPUT_DIR|g" "$TEMPLATES/warmup_task.txt" > "$WARMUP_TASK"

log "Creating base session..."
log "Output: $OUTPUT_DIR"

# Run warmup with default system prompt (baseline)
cd "$OUTPUT_DIR"
timeout 10m "$AI_BIN" \
    --mode headless \
    --timeout 10m \
    --max-turns 30 \
    "$(cat "$WARMUP_TASK")" \
    > "$OUTPUT_DIR/warmup_output.txt" 2>&1 || true
cd "$PROJECT_DIR"

# Extract session info
SESSION_ID=""
SESSION_FILE=""
if grep -q "Session ID:" "$OUTPUT_DIR/warmup_output.txt" 2>/dev/null; then
    SESSION_ID=$(grep "Session ID:" "$OUTPUT_DIR/warmup_output.txt" | head -1 | sed 's/.*Session ID: //' | tr -d '[:space:]')
    SESSION_FILE=$(grep "Session file:" "$OUTPUT_DIR/warmup_output.txt" | head -1 | sed 's/.*Session file: //' | tr -d '[:space:]')
fi

if [[ -z "$SESSION_ID" ]]; then
    echo "[FATAL] Failed to create base session" >&2
    exit 1
fi

# Freeze session
BASE_DIR="$OUTPUT_DIR/base_session"
mkdir -p "$BASE_DIR"

if [[ -f "$SESSION_FILE" ]]; then
    cp "$SESSION_FILE" "$BASE_DIR/messages.jsonl"
else
    echo "[FATAL] Session file not found: $SESSION_FILE" >&2
    exit 1
fi

# Copy trace if available
TRACE=$(ls -t "$HOME/.ai/traces/"*"*$SESSION_ID"* 2>/dev/null | head -1 || true)
if [[ -n "$TRACE" ]]; then
    cp "$TRACE" "$BASE_DIR/warmup_trace.json"
fi

MSG_COUNT=$(wc -l < "$BASE_DIR/messages.jsonl" | tr -d ' ')
SIZE=$(du -h "$BASE_DIR/messages.jsonl" | cut -f1)

log "Base session created:"
log "  ID:       $SESSION_ID"
log "  Messages: $MSG_COUNT"
log "  Size:     $SIZE"
log "  Path:     $BASE_DIR/messages.jsonl"
log ""
log "Ready for evolve loop."
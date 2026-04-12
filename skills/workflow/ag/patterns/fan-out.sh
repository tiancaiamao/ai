#!/bin/bash
# fan-out.sh — One task split into N subtasks, executed in parallel, then merged
#
# Usage: fan-out.sh <plan-file> <worker-count> <worker-prompt> <merger-prompt>
#
# 1. Spawns workers that each claim tasks from the task queue
# 2. Workers execute tasks in parallel (using ag task claim for coordination)
# 3. When all tasks are done, a merger agent collects all results
#
# Expects: The plan-file should contain a list of tasks that can be claimed.
#          You should pre-create tasks with `ag task create` before running this.
#
# Alternative simple usage:
#   fan-out.sh <plan-file> <worker-count> <worker-prompt> <merger-prompt>
#   This will create one task per line in plan-file, spawn N workers,
#   and merge results.
#
# Environment:
#   AG_BIN — path to ag binary (default: ag)
#
set -euo pipefail

AG_BIN="${AG_BIN:-ag}"
MOCK="${AG_MOCK:-}"
PLAN_FILE="$1"
WORKER_COUNT="$2"
WORKER_PROMPT="$3"
MERGER_PROMPT="$4"

echo "[fan-out] Plan: $PLAN_FILE"
echo "[fan-out] Workers: $WORKER_COUNT"

# --- Create tasks from plan file ---
TASK_IDS=()
while IFS= read -r line; do
  # Skip empty lines and comments
  [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
  TASK_ID=$($AG_BIN task create "$line")
  TASK_IDS+=("$TASK_ID")
done < "$PLAN_FILE"

TOTAL_TASKS=${#TASK_IDS[@]}
echo "[fan-out] Created $TOTAL_TASKS tasks"

if [ "$TOTAL_TASKS" -eq 0 ]; then
  echo "[fan-out] No tasks to execute"
  exit 0
fi

# --- Spawn worker pool ---
WORKER_PIDS=()
WORKER_IDS=()

for i in $(seq 1 "$WORKER_COUNT"); do
  (
    WORKER_ID="fanout-worker-$$-$i"
    while true; do
      # Try to claim a pending task
      TASK_LINE=$($AG_BIN task list --status pending 2>/dev/null | tail -n +2 | head -1) || break
      [ -z "$TASK_LINE" ] && break

      TASK_ID=$(echo "$TASK_LINE" | awk '{print $1}')
      if ! $AG_BIN task claim "$TASK_ID" --as "$WORKER_ID" 2>/dev/null; then
        # Race: another worker claimed it first, retry
        sleep 1
        continue
      fi

      # Get task description
      TASK_DESC=$(echo "$TASK_LINE" | awk '{$1=$2=$3=""; print substr($0,4)}')

      # Spawn agent for this task
      AGENT_ID="fanout-$$-task-$TASK_ID"
      if [ -n "$MOCK" ]; then
        $AG_BIN spawn --id "$AGENT_ID" --mock --mock-script "$WORKER_PROMPT" --input <(echo "$TASK_DESC") --timeout 10m
      else
        $AG_BIN spawn --id "$AGENT_ID" --system "$WORKER_PROMPT" --input <(echo "$TASK_DESC") --timeout 10m
      fi

      if $AG_BIN wait "$AGENT_ID" --timeout 600; then
        OUTPUT=$(mktemp)
        $AG_BIN output "$AGENT_ID" > "$OUTPUT"
        $AG_BIN task done "$TASK_ID" --output "$OUTPUT"
        rm -f "$OUTPUT"
      else
        $AG_BIN task fail "$TASK_ID" --error "worker timed out"
      fi
    done
  ) &
  WORKER_PIDS+=($!)
done

# Wait for all workers
for pid in "${WORKER_PIDS[@]}"; do
  wait "$pid" 2>/dev/null || true
done

# --- Check results ---
DONE_COUNT=$($AG_BIN task list --status done 2>/dev/null | wc -l | tr -d ' ')
FAIL_COUNT=$($AG_BIN task list --status failed 2>/dev/null | wc -l | tr -d ' ')
echo "[fan-out] Results: $DONE_COUNT done, $FAIL_COUNT failed"

if [ "$DONE_COUNT" -eq 0 ]; then
  echo "[fan-out] ❌ No tasks completed"
  exit 1
fi

# --- Merge results ---
MERGER_ID="fanout-merger-$$"
RESULTS_FILE=$(mktemp)

# Collect all task outputs
for tid in "${TASK_IDS[@]}"; do
  TASK_INFO=$($AG_BIN task show "$tid" 2>/dev/null)
  STATUS=$(echo "$TASK_INFO" | grep "^status:" | awk '{print $2}')
  if [ "$STATUS" = "done" ]; then
    OUTPUT_PATH=$(echo "$TASK_INFO" | grep "^output:" | awk '{print $2}')
    echo "--- Task $tid ---" >> "$RESULTS_FILE"
    cat "$OUTPUT_PATH" >> "$RESULTS_FILE" 2>/dev/null || echo "(no output)" >> "$RESULTS_FILE"
    echo "" >> "$RESULTS_FILE"
  fi
done

echo "[fan-out] Merging results..."
if [ -n "$MOCK" ]; then
  $AG_BIN spawn --id "$MERGER_ID" --mock --mock-script "$MERGER_PROMPT" --input "$RESULTS_FILE" --timeout 5m
else
  $AG_BIN spawn --id "$MERGER_ID" --system "$MERGER_PROMPT" --input "$RESULTS_FILE" --timeout 5m
fi

if $AG_BIN wait "$MERGER_ID" --timeout 300; then
  $AG_BIN output "$MERGER_ID"
  echo ""
  echo "[fan-out] ✅ Complete"
else
  echo "[fan-out] ❌ Merge failed"
  exit 1
fi

rm -f "$RESULTS_FILE"

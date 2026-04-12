#!/bin/bash
# test-patterns.sh — Verify all patterns with mock agents
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
AG="$SCRIPT_DIR/../ag"
PATTERNS="$SCRIPT_DIR/../patterns"
MOCKS="$SCRIPT_DIR/mocks"
WORKSPACE=$(mktemp -d)

export AG_BIN="$AG"
export AG_MOCK=1

# Clean up any leftover state from previous runs
rm -f /tmp/ag-test-judge-multi-round

cd "$WORKSPACE"
echo "Workspace: $WORKSPACE"

# ============================================================
echo ""
echo "===== TEST 1: pair.sh — immediate approval ====="
echo "=================================================="
echo "write a hello world function" > input.txt
RESULT=$($PATTERNS/pair.sh \
  "$MOCKS/worker-mock.sh" \
  "$MOCKS/judge-mock.sh" \
  input.txt 2)

echo "Result:"
echo "$RESULT"

# Verify: output should contain "WORKED:"
if echo "$RESULT" | grep -q "WORKED:"; then
  echo "✅ TEST 1 PASSED: worker-judge approved immediately"
else
  echo "❌ TEST 1 FAILED: expected WORKED: in output"
  exit 1
fi

# Cleanup
rm -rf .ag

# ============================================================
echo ""
echo "===== TEST 2: pair.sh — multi-round (3 rounds to approval) ====="
echo "=================================================================="
echo "write a hello world function" > input2.txt

RESULT=$($PATTERNS/pair.sh \
  "$MOCKS/worker-mock.sh" \
  "$MOCKS/judge-reject-mock.sh" \
  input2.txt 5 2>&1)

echo "$RESULT"

if echo "$RESULT" | grep -q "APPROVED"; then
  echo "✅ TEST 2 PASSED: eventually approved after multiple rounds"
else
  echo "❌ TEST 2 FAILED: expected eventual approval"
  exit 1
fi

rm -rf .ag

# ============================================================
echo ""
echo "===== TEST 3: parallel.sh — 3 agents ====="
echo "============================================="
$PATTERNS/parallel.sh 3 "$MOCKS/explorer-mock.sh" "investigate auth module" "$WORKSPACE/parallel-out"

# Verify: 3 output files, each with different index
if [ -f "$WORKSPACE/parallel-out/agent-0.md" ] && \
   [ -f "$WORKSPACE/parallel-out/agent-1.md" ] && \
   [ -f "$WORKSPACE/parallel-out/agent-2.md" ]; then
  echo "✅ TEST 3 PASSED: 3 agents produced output"
else
  echo "❌ TEST 3 FAILED: missing output files"
  exit 1
fi

# Verify different results per agent
A0=$(grep "Result 0" "$WORKSPACE/parallel-out/agent-0.md" || true)
A1=$(grep "Result 1" "$WORKSPACE/parallel-out/agent-1.md" || true)
A2=$(grep "Result 2" "$WORKSPACE/parallel-out/agent-2.md" || true)
if [ -n "$A0" ] && [ -n "$A1" ] && [ -n "$A2" ]; then
  echo "✅ TEST 3 DETAIL: each agent got unique index"
else
  echo "⚠️  TEST 3 WARNING: agents may not have unique indices"
fi

rm -rf .ag

# ============================================================
echo ""
echo "===== TEST 4: pipeline.sh — 3 stages ====="
echo "============================================"
echo "initial data" > pipeline-input.txt

RESULT=$($PATTERNS/pipeline.sh \
  pipeline-input.txt \
  "$MOCKS/stage-mock.sh" \
  "$MOCKS/stage-mock.sh" \
  "$MOCKS/stage-mock.sh")

echo "Pipeline result:"
echo "$RESULT"

# Verify: should have 3 "Processed by" markers (3 stages)
COUNT=$(echo "$RESULT" | grep -c "Processed by" || true)
if [ "$COUNT" -eq 3 ]; then
  echo "✅ TEST 4 PASSED: 3 stages ran in sequence"
else
  echo "❌ TEST 4 FAILED: expected 3 'Processed by' markers, got $COUNT"
  exit 1
fi

# Verify: initial data is preserved through pipeline
if echo "$RESULT" | grep -q "initial data"; then
  echo "✅ TEST 4 DETAIL: data passed through all stages"
else
  echo "❌ TEST 4 FAILED: data lost in pipeline"
  exit 1
fi

rm -rf .ag

# ============================================================
echo ""
echo "===== TEST 5: task + claim — concurrent safety ====="
echo "====================================================="
$AG task create "Task A"
$AG task create "Task B"
$AG task create "Task C"

# Claim t001 by worker-1
$AG task claim t001 --as worker-1
# Try to claim t001 again by worker-2 (should fail)
set +e
$AG task claim t001 --as worker-2 2>/dev/null
CLAIM_EXIT=$?
set -e

if [ "$CLAIM_EXIT" -ne 0 ]; then
  echo "✅ TEST 5 PASSED: double-claim prevented"
else
  echo "❌ TEST 5 FAILED: double-claim should have been rejected"
  exit 1
fi

# Verify t001 is claimed by worker-1
OWNER=$($AG task show t001 | grep "^claimant:" | awk '{print $2}')
if [ "$OWNER" = "worker-1" ]; then
  echo "✅ TEST 5 DETAIL: correct owner"
else
  echo "❌ TEST 5 FAILED: wrong owner: $OWNER"
  exit 1
fi

rm -rf .ag

# ============================================================
echo ""
echo "===== TEST 6: channel send/recv — FIFO order ====="
echo "==================================================="
$AG channel create fifo-test
echo "first" | $AG send fifo-test
echo "second" | $AG send fifo-test
echo "third" | $AG send fifo-test

FIRST=$($AG recv fifo-test)
SECOND=$($AG recv fifo-test)
THIRD=$($AG recv fifo-test)

if [ "$FIRST" = "first" ] && [ "$SECOND" = "second" ] && [ "$THIRD" = "third" ]; then
  echo "✅ TEST 6 PASSED: FIFO order preserved"
else
  echo "❌ TEST 6 FAILED: got '$FIRST', '$SECOND', '$THIRD'"
  exit 1
fi

rm -rf .ag

# ============================================================
echo ""
echo "======================================="
echo "✅ ALL 6 PATTERN TESTS PASSED"
echo "======================================="

# Cleanup
rm -rf "$WORKSPACE"
#!/bin/bash
set -e

TASK_DIR="$(cd "$(dirname "$0")" && pwd)"

# Ensure setup/ exists with a fresh copy of init
if [ ! -d "$TASK_DIR/setup" ] || [ -z "$(ls -A "$TASK_DIR/setup" 2>/dev/null)" ]; then
  rm -rf "$TASK_DIR/setup"
  cp -R "$TASK_DIR/init" "$TASK_DIR/setup"
fi

cd "$TASK_DIR/setup"

# Check that the agent created a test file
if [ ! -f "test_export_flows.py" ]; then
  echo "FAIL: test_export_flows.py not found"
  exit 1
fi

# Install test dependencies
pip install pytest pytest-cov -q 2>/dev/null

# conftest.py mocks mitmproxy so no need to install it
PYTHONPATH="$TASK_DIR/setup" python3 -m pytest test_export_flows.py \
  --cov=export_flows \
  --cov-report=term-missing \
  --cov-fail-under=90 \
  -v

echo ""
echo "All tests passed with ≥90% coverage!"
exit 0
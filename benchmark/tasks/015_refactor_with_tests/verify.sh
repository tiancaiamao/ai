#!/bin/bash
set -e

TASK_DIR="$(cd "$(dirname "$0")" && pwd)"

# Ensure setup/ exists with a fresh copy of init
if [ ! -d "$TASK_DIR/setup" ] || [ -z "$(ls -A "$TASK_DIR/setup" 2>/dev/null)" ]; then
  rm -rf "$TASK_DIR/setup"
  cp -R "$TASK_DIR/init" "$TASK_DIR/setup"
fi

cd "$TASK_DIR/setup"

# Install test dependencies
pip install pytest pytest-cov -q 2>/dev/null

# conftest.py mocks mitmproxy so no need to install it

# Test 1: All existing tests must pass
echo "--- Running tests ---"
PYTHONPATH="$TASK_DIR/setup" python3 -m pytest test_export_flows.py -v

# Test 2: export_flows must still be importable
echo "--- Checking backward compatibility ---"
python3 -c "
import export_flows
# Verify key public functions still exist
assert callable(export_flows.count_whitespace_stats)
assert callable(export_flows.get_canonical_model)
assert callable(export_flows.get_pricing)
assert callable(export_flows.sanitize_path)
assert callable(export_flows.extract_stop_reason)
assert callable(export_flows.calculate_request_cost)
assert callable(export_flows.format_headers)
assert callable(export_flows.parse_request_body)
assert callable(export_flows.write_request)
assert callable(export_flows.write_response)
assert callable(export_flows.export_flows)
assert callable(export_flows.main)
print('PASS: All public functions still accessible via import export_flows')
"

# Test 3: Code must be split into multiple files
echo "--- Checking refactoring ---"
python3 -c "
import os
import glob

# Count .py files (excluding __pycache__, test files, and the stub export_flows.py)
py_files = [f for f in glob.glob('*.py')
            if not f.startswith('test_')
            and not f.startswith('__')]
if len(py_files) < 3:
    print(f'FAIL: Expected at least 3 .py modules, found {len(py_files)}: {py_files}')
    exit(1)
print(f'PASS: Found {len(py_files)} Python modules: {py_files}')
"

echo ""
echo "All refactoring checks passed!"
exit 0
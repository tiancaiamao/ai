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

# ============================================================
# Test 1: All existing tests must pass
# ============================================================
echo "--- Running tests ---"
PYTHONPATH="$TASK_DIR/setup" python3 -m pytest test_export_flows.py -v

# Pre-load mitmproxy mocks for standalone python invocations
_preload_script="$TASK_DIR/setup/conftest.py"
_mock_preamble="import sys; exec(open('$_preload_script').read())"

# ============================================================
# Test 2: Backward compatibility — all public functions accessible
# ============================================================
echo "--- Checking backward compatibility ---"
python3 -c "
$_mock_preamble
import export_flows
for fn in [
    'count_whitespace_stats', 'get_canonical_model', 'get_pricing',
    'sanitize_path', 'extract_stop_reason', 'calculate_costs',
    'format_headers', 'parse_request_body', 'write_request',
    'write_response', 'export_flows', 'main',
    'extract_usage', 'extract_prompts', 'extract_source_attribution',
    'redact_flow_files', 'summarize_usage', 'attribute_tokens',
]:
    assert callable(getattr(export_flows, fn, None)), f'export_flows.{fn} missing or not callable'
print('PASS: All public functions accessible via import export_flows')
"

# ============================================================
# Test 3: Module split — at least 4 non-test .py files
# ============================================================
echo "--- Checking module split ---"
python3 -c "
import os, glob
py_files = sorted(f for f in glob.glob('*.py')
                   if not f.startswith('test_') and not f.startswith('__'))
if len(py_files) < 4:
    print(f'FAIL: Expected ≥4 .py modules, found {len(py_files)}: {py_files}')
    exit(1)
print(f'PASS: Found {len(py_files)} Python modules: {py_files}')
"

# ============================================================
# Test 4: No duplicated os.walk blocks — must use shared helper
# ============================================================
echo "--- Checking os.walk dedup ---"
python3 -c "
import os, glob

# Count raw os.walk calls across all non-test .py files
walk_count = 0
for f in glob.glob('*.py'):
    if f.startswith('test_'): continue
    content = open(f).read()
    walk_count += content.count('os.walk(')

if walk_count > 1:
    print(f'FAIL: Found {walk_count} os.walk() calls — extract a shared directory walker')
    exit(1)
print(f'PASS: os.walk centralized ({walk_count} call site)')
"

# ============================================================
# Test 5: No duplicated json.dump + f.write patterns
# ============================================================
echo "--- Checking json.dump dedup ---"
python3 -c "
import glob, re

# Look for inline json.dump(..., f, indent=2) patterns outside of any helper
# A helper is fine — raw inline calls scattered across functions are not
inline_dumps = 0
for f in glob.glob('*.py'):
    if f.startswith('test_'): continue
    content = open(f).read()
    # Count json.dump calls that are NOT inside a function named write_json or save_json
    inline_dumps += len(re.findall(r'json\.dump\(', content))

# If there are many json.dump calls, they should be concentrated in 1-2 helper functions
# not scattered. We allow up to 3 total (one in a helper, maybe one in main, one edge case)
if inline_dumps > 4:
    print(f'FAIL: Found {inline_dumps} json.dump() calls — extract a write_json helper')
    exit(1)
print(f'PASS: json.dump calls under control ({inline_dumps} total)')
"

# ============================================================
# Test 6: No function exceeds 80 lines
# ============================================================
echo "--- Checking function length ---"
python3 -c "
import ast, glob, sys

violations = []
for f in sorted(glob.glob('*.py')):
    if f.startswith('test_'): continue
    try:
        tree = ast.parse(open(f).read())
    except SyntaxError:
        continue
    for node in ast.walk(tree):
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            # end_lineno is available in Python 3.8+
            if hasattr(node, 'end_lineno') and node.end_lineno:
                length = node.end_lineno - node.lineno + 1
                if length > 80:
                    violations.append(f'{f}:{node.name} ({length} lines, L{node.lineno}-L{node.end_lineno})')
if violations:
    print('FAIL: Functions exceeding 80 lines:')
    for v in violations:
        print(f'  {v}')
    exit(1)
print('PASS: All functions ≤80 lines')
"

# ============================================================
# Test 7: Total code ≤1500 lines (no bloat)
# ============================================================
echo "--- Checking total line count ---"
python3 -c "
import glob
total = sum(
    sum(1 for _ in open(f))
    for f in glob.glob('*.py')
    if not f.startswith('test_') and not f.startswith('__')
)
if total > 1500:
    print(f'FAIL: Total {total} lines exceeds 1500 — refactoring added bloat')
    exit(1)
print(f'PASS: Total {total} lines (≤1500)')
"

echo ""
echo "All refactoring checks passed!"
exit 0
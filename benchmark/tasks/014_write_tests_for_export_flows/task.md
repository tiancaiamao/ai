# Task: Write Tests for Export Flows

## Description

The file `export_flows.py` is a ~1400-line Python module that processes mitmproxy flow files. Your job is to write a comprehensive pytest test suite that achieves **≥80% code coverage**.

## Requirements

1. Create `test_export_flows.py` in the same directory as `export_flows.py`
2. Use `pytest` with `unittest.mock` for filesystem and mitmproxy dependencies
3. Focus on testing pure logic functions with various inputs, edge cases, and error paths
4. Mock all external dependencies (mitmproxy, filesystem writes) — do NOT require real `.flow` files
5. Coverage must be ≥80% (measured by `pytest-cov`)

## Key Functions to Test

The module contains many testable functions that don't require mitmproxy:

- `count_whitespace_stats(text)` — pure string analysis
- `parse_request_body(path)` — JSON file reading with error handling
- `get_canonical_model(model_id)` / `get_pricing(model_id)` — model name matching
- `sanitize_path(path)` — URL path sanitization
- `extract_stop_reason(response_raw)` — SSE/JSON parsing
- `calculate_request_cost(model, usage)` — cost computation
- `format_headers(headers)` — header formatting with redaction

For functions that depend on mitmproxy (`FlowReader`, `HTTPFlow`, etc.), use `unittest.mock.MagicMock(spec=HTTPFlow)`.

## Constraints

- Do NOT modify `export_flows.py`
- All tests must be self-contained (no external files needed at test time)
- Use `pytest` fixtures for common setup

## Success Criteria

- `pytest test_export_flows.py` passes with all tests green
- `pytest --cov=export_flows test_export_flows.py` shows ≥80% coverage
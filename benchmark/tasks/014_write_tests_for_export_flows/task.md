# Task: Write Tests for Export Flows

## Description

The file `export_flows.py` is a ~1400-line Python module that processes mitmproxy flow files. Your job is to write a comprehensive pytest test suite that achieves **≥90% code coverage**.

## Requirements

1. Create `test_export_flows.py` in the same directory as `export_flows.py`
2. Use `pytest` with `unittest.mock` for filesystem and mitmproxy dependencies
3. Test all functions — both the easy pure-logic ones AND the harder functions that interact with mitmproxy / filesystem
4. Mock all external dependencies (mitmproxy, filesystem writes) — do NOT require real `.flow` files
5. Coverage must be ≥90% (measured by `pytest-cov`)

## Functions to Test

**Pure logic (easy to test, but don't stop here):**
- `count_whitespace_stats(text)` — string analysis
- `get_canonical_model(model_id)` / `get_pricing(model_id)` — model name matching
- `sanitize_path(path)` — URL path sanitization
- `extract_stop_reason(response_raw)` — SSE/JSON parsing
- `calculate_request_cost(model, usage)` — cost computation
- `format_headers(headers)` — header formatting with redaction

**Harder functions (where coverage gaps live):**
- `parse_request_body(path)` — needs filesystem mock
- `write_request(flow_dir, flow)` — needs MagicMock(spec=HTTPFlow) for flow
- `write_response(flow_dir, flow)` — needs MagicMock(spec=HTTPFlow) for flow
- `redact_flow_files(directory)` — needs FlowReader mock + .flow file mock
- `export_flows(input_dir, output_dir)` — end-to-end flow
- `extract_usage(output_dir)` — directory walking + JSON parsing
- `calculate_costs(output_dir)` — model pricing + token math
- `summarize_usage(output_dir)` — aggregation across agent directories

## Tips

- Use `unittest.mock.patch` to mock `builtins.open` for filesystem tests
- Use `unittest.mock.MagicMock(spec=HTTPFlow)` for mitmproxy flow objects
- Use `tmp_path` pytest fixture for tests that write files
- Group related tests into classes for clarity

## Constraints

- Do NOT modify `export_flows.py`
- All tests must be self-contained (no external files needed at test time)
- Do NOT import or copy test code from other test suites that may exist nearby

## Success Criteria

- `pytest test_export_flows.py` passes with all tests green
- `pytest --cov=export_flows test_export_flows.py` shows ≥90% coverage
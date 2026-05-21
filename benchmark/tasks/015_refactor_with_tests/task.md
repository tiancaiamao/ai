# Task: Refactor Export Flows with Test Protection

## Description

The file `export_flows.py` is a ~1400-line Python module that needs structural refactoring. A comprehensive test suite is already provided in `test_export_flows.py`. Your job is to refactor the code for better structure and maintainability **while keeping all tests passing**.

## Current Problems

1. **Monolithic file** (~1400 lines) mixing 5+ distinct concerns in one file:
   - Pure utility functions (string stats, path sanitization)
   - Model pricing / cost logic
   - Mitmproxy flow parsing and writing
   - Usage extraction and aggregation
   - CLI argument parsing and `main()`

2. **Duplicated patterns** scattered across functions:
   - 5+ `os.walk()` directory traversal blocks with nearly identical structure
   - 3+ `json.dump(..., f, indent=2); f.write("\n")` usage-file writes
   - Repeated `json.loads(line)` SSE line parsing in `extract_usage`, `extract_prompts`, `extract_source_attribution`, and `calculate_costs`
   - Duplicated token-counting logic across `attribute_tokens` and `aggregate_by_source`

3. **Hard-coded configuration** (pricing table, agent colors)

4. **Overly long functions** — several exceed 80 lines

## Refactoring Goals

1. Split the monolith into focused modules (e.g., `pricing.py`, `parsers.py`, `writers.py`, `costs.py`, `utils.py`)
2. Keep `export_flows.py` as the public entry point that re-exports everything (backward compatible)
3. Extract hard-coded configuration into a config module or data file
4. **Eliminate duplicated code** — extract shared patterns into utility functions:
   - A single directory-walker helper (replaces the 5+ `os.walk` blocks)
   - A single `write_json(path, data)` helper (replaces the repeated json.dump+write pattern)
   - A single SSE line parser (replaces the scattered `json.loads(line)` loops)
   - Unify token-counting logic
5. Simplify overly long functions — no function may exceed 80 lines
6. Improve naming where unclear

## Constraints

- **Do NOT modify `test_export_flows.py`** — it's your safety net
- **All tests must pass** after each refactoring step
- **The public API must remain identical**: `import export_flows` and all existing function calls must work
- Do NOT change any external behavior — output of any function given the same input must be identical
- Do NOT import or copy code from other directories

## Success Criteria

- `pytest test_export_flows.py` passes with all tests green
- The original `export_flows.py` can still be imported directly: `import export_flows`
- Code is split into multiple files/modules (at least 4, not counting test files)
- No function exceeds 80 lines
- No duplicated `os.walk` blocks — must use a shared helper
- No duplicated `json.dump + f.write` patterns — must use a shared helper
- Total non-test Python code must be ≤1500 lines (no code bloat from refactoring)
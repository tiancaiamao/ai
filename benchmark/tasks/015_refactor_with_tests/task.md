# Task: Refactor Export Flows with Test Protection

## Description

The file `export_flows.py` is a ~1400-line Python module that needs structural refactoring. A comprehensive test suite is already provided in `test_export_flows.py`. Your job is to refactor the code for better structure and maintainability **while keeping all tests passing**.

## Current Problems

- Single monolithic file (~1400 lines) mixing concerns:
  - Pure utility functions (string stats, path sanitization)
  - Model pricing logic
  - Mitmproxy flow parsing and writing
  - Cost calculation and aggregation
  - CLI argument parsing and `main()`
- Hard-coded configuration (pricing table, agent colors)
- Some functions are too long and do multiple things
- Global mutable state via module-level constants

## Refactoring Goals

1. Split the monolith into focused modules (e.g., `pricing.py`, `parsers.py`, `writers.py`, `costs.py`, `utils.py`)
2. Keep `export_flows.py` as the public entry point that re-exports everything (backward compatible)
3. Extract hard-coded configuration into a config module or data file
4. Simplify overly long functions
5. Improve naming where unclear

## Constraints

- **Do NOT modify `test_export_flows.py`** — it's your safety net
- **All tests must pass** after each refactoring step
- **The public API must remain identical**: `import export_flows` and all existing function calls must work
- Do NOT change any external behavior — output of any function given the same input must be identical

## Success Criteria

- `pytest test_export_flows.py` passes with all tests green
- The original `export_flows.py` can still be imported directly: `import export_flows`
- Code is split into multiple files/modules (at least 3)
- No function exceeds 80 lines
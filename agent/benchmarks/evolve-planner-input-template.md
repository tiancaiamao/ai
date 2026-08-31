# Evolve Planner Input

---

## Current Iteration Overview

{{CURRENT_OVERVIEW}}

## Task Classification

{{TASK_CLASSIFICATION}}

## Failure Analysis

{{FAILURE_DETAILS}}

## AI Debugger Analysis

{{DEBUGGER_ANALYSIS}}

## Cross-Iteration Task Results

{{CROSS_ITERATION_TABLE}}

## Cross-Iteration Changes (vs Baseline)

{{CROSS_ITERATION_CHANGES}}

## Historical Trends

{{HISTORICAL_TRENDS}}

## Task Stability

{{TASK_STABILITY}}

## Previous Change Attribution

{{PREVIOUS_ATTRIBUTION}}

## Previous Change Verdict

{{ATTRIBUTION_VERDICT}}

## Strategy History

{{STRATEGY_HISTORY}}

## Prompt Length Budget

Current sizes (the combined budget for `system_prompt.md` + `memory.md` is **8 KB**):

{{PROMPT_LENGTH_REPORT}}

## Benchmark Evaluation Mechanics

The benchmark scores tasks on two dimensions:

1. **Functional correctness** — does `verify.sh` exit 0?
2. **Agentic score (0-100)** — computed from the agent's trace, based on:
   - **Test-first discipline**: did the agent run tests BEFORE the first edit?
     (heuristic: any bash command matching `pytest|go test|npm test|cargo test|unittest|verify.sh`)
   - **Investigation depth**: did the agent run tests at least twice and use
     `grep` to search before reading many files?
   - **No fix-first-error**: did the agent investigate beyond the first
     reported error? (especially important when the first error is misleading)
   - **Tool-loop avoidance**: did the agent call the same tool more than 5
     times without progress?

A task can pass `functional_passed=true` but `agentic_passed=false`, and vice
versa. Both must be true for `passed=true`.

**When proposing changes**, think about which of these signals your rule
strengthens or weakens.

## Current Harness Files

The current agent configuration and harness files live in `agent/` relative
to the project root. Use the `read` tool to inspect them directly:

- **Agent config**: `{{AGENT_CONFIG_PATH}}`
- **System prompt**: `{{SYSTEM_PROMPT_PATH}}`
- **Memory**: `{{MEMORY_PATH}}`
- **Context management**: `{{CONTEXT_MANAGEMENT_PATH}}`

Do NOT inline the full contents here — read the files directly so you have
up-to-date content.
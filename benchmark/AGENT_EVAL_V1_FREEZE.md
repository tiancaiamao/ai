# Agent Eval V1 Freeze

## Freeze Date
- 2026-03-19

## Scope
This freeze defines the stable `v1` agent evaluation set for A/B comparison.

## Frozen Task Set (11)
1. `agent_001_forced_exploration`
2. `agent_002_rollback`
3. `agent_003_hidden_dep`
4. `agent_004_context_overflow`
5. `agent_005_delayed_signal`
6. `agent_006_tool_trap`
7. `agent_007_misleading`
8. `agent_008_budget`
9. `agent_009_partial_info`
10. `agent_010_memory`
11. `agent_011_compact_tool_call_mismatch`

## Scoring Policy (V1)
- Global `max_steps_mode`: `soft`
- Per-task override:
  - `agent_008_budget`: `max_steps_mode = hard`
- Primary reported dimensions:
  - `functional_pass_rate`
  - `agentic_pass_rate`
  - `avg_agentic_score`
- `passed` remains the final gate (`functional + hard constraints`).

## Stability Rules
- Do not change task semantics, fixtures, or assertions inside v1 tasks.
- If a task must change for correctness, create `v1.x` patch notes.
- New tasks should be added in `v2` scope unless explicitly approved.

## Repro Baseline Command
```bash
cd benchmark/benchmark
make bench-run AGENT=/path/to/agent MAX_STEPS_MODE=soft TIMEOUT=0
```

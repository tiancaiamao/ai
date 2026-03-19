# Agent Eval Comparison Report (V1)

## Metadata
- Date: `{{date}}`
- Task Set Version: `v1`
- Baseline Agent: `{{baseline_agent}}`
- Candidate Agent: `{{candidate_agent}}`
- Baseline Run: `{{baseline_run_path}}`
- Candidate Run: `{{candidate_run_path}}`

## Summary Metrics
| Metric | Baseline | Candidate | Delta |
|---|---:|---:|---:|
| pass_rate | {{baseline.pass_rate}} | {{candidate.pass_rate}} | {{delta.pass_rate}} |
| functional_pass_rate | {{baseline.functional_pass_rate}} | {{candidate.functional_pass_rate}} | {{delta.functional_pass_rate}} |
| agentic_pass_rate | {{baseline.agentic_pass_rate}} | {{candidate.agentic_pass_rate}} | {{delta.agentic_pass_rate}} |
| avg_agentic_score | {{baseline.avg_agentic_score}} | {{candidate.avg_agentic_score}} | {{delta.avg_agentic_score}} |

## Per-Task Comparison
| Task | Baseline Overall | Candidate Overall | Baseline Functional | Candidate Functional | Baseline Agentic Score | Candidate Agentic Score | Notes |
|---|---|---|---|---|---:|---:|---|
| agent_001_forced_exploration | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | |
| agent_002_rollback | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | |
| agent_003_hidden_dep | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | |
| agent_004_context_overflow | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | |
| agent_005_delayed_signal | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | |
| agent_006_tool_trap | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | |
| agent_007_misleading | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | |
| agent_008_budget | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | hard max_steps |
| agent_009_partial_info | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | |
| agent_010_memory | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | |
| agent_011_compact_tool_call_mismatch | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | {{...}} | trace-driven |

## Failure Breakdown (Candidate)
- Hard violations (top):
  - `{{violation_1}}`
  - `{{violation_2}}`
- Soft violations (top):
  - `{{soft_1}}`
  - `{{soft_2}}`

## Key Findings
1. `{{finding_1}}`
2. `{{finding_2}}`
3. `{{finding_3}}`

## Decision
- Verdict: `{{ship_or_not}}`
- Rationale: `{{rationale}}`

## Reproduction Commands
```bash
{{baseline_cmd}}
{{candidate_cmd}}
```

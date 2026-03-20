# Agent Case Authoring Guide

This guide documents how to add and maintain agent-focused benchmark tasks.

## Scope

These tasks are designed to measure **agent behavior** (tool use, planning, validation, recovery), not raw model trivia recall.

## Task Layout

Each task lives under `tasks/<task_id>/` and should include:

- `task.md`: user-facing task prompt.
- `init/`: deterministic initial workspace copied into `setup/` before each run.
- `verify.sh`: functional verifier (exit code `0` means functional pass).
- `constraints.json` (optional but recommended): process-level agent checks.

Runner behavior:

1. `init/` is copied to `setup/`.
2. Agent runs in `setup/`.
3. `verify.sh` runs from the task root.
4. `constraints.json` (if present) evaluates agent process quality.

## Design Principles

1. Keep failures deterministic. Avoid flaky network/time dependencies.
2. Force real investigation. The shortest path should require reading/search/testing.
3. Reward behavior signals. Use constraints to capture strategy quality.
4. Keep fixes local and minimal. Do not require broad refactors unless intentional.
5. Separate functional correctness from agent quality.

## `constraints.json` Tips

Common fields:

- `max_steps`: allowed tool-call budget.
- `max_steps_mode`: `soft` or `hard`.
- `must_use_capabilities`: e.g. `search`, `read`, `edit`, `test`, `rollback`.
- `forbidden_patterns`: anti-pattern detectors (guessing, shallow fix, etc.).
- `success_criteria`: task-specific process assertions.

Recommended policy:

- Use `soft` max-steps for most tasks (score penalty only).
- Use `hard` only when strict budget is core to the task itself.

## Naming and Versioning

- Use stable IDs: `agent_001_xxx`, `agent_002_xxx`, ...
- Do not renumber existing frozen tasks.
- Add new tasks with new IDs and update manifest explicitly.

## Frozen Set via Manifest

Use `tasks/agent_v1_manifest.json` to freeze an evaluation set.

- `tasks`: exact ordered list of task IDs.
- `global_defaults.max_steps_mode`: default score policy for the set.

Examples:

```bash
# List only frozen v1 tasks
make bench-list MANIFEST=tasks/agent_v1_manifest.json

# Run frozen v1 task set
make bench-run MANIFEST=tasks/agent_v1_manifest.json

# Run one task within frozen set
make bench-run TASK=agent_011_compact_tool_call_mismatch MANIFEST=tasks/agent_v1_manifest.json
```

## Validation Checklist Before Merge

1. `make bench-list` shows the new task.
2. Initial state fails `verify.sh` (when run on `init` copied to `setup`).
3. Expected fix makes `verify.sh` pass.
4. Constraints are meaningful and not overfit to one agent.
5. `go test ./cmd/benchmark -v` passes.
6. If task belongs to frozen set, update manifest and freeze notes.

## Trace-Driven Bug Cases

For runtime/protocol regressions (like compact/tool-call mismatch):

1. Save evidence artifact in `init/trace/`.
2. Add a focused unit/integration test that fails from that evidence.
3. Require minimal production fix in `task.md`.
4. Forbid changing fixtures unless explicitly intended.

This pattern keeps cases realistic while still verifiable and deterministic.

## Legacy `/app` Compatibility

Some imported Terminal Bench tasks reference absolute `/app/...` paths.

The runner provides a compatibility shim and rewrites these references to the task's runtime `setup` directory, so cases can run without requiring `sudo` or creating a real `/app` directory.

For new tasks, prefer relative or workspace-local paths to avoid hidden environment assumptions.

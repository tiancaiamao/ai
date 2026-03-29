---
name: orchestrate
description: Backend Go CLI for workflow execution. This skill is an internal runtime used by the workflow skill and worker processes.
allowed-tools: [bash]
---

# Orchestrate Backend (Internal)

`orchestrate` is the backend CLI runtime for the `workflow` skill.

It is **not** intended to be a user-facing planning/delegation skill.
Use `/workflow ...` commands as the frontend entrypoint.

## Role in Architecture

`/workflow ...` (frontend) calls:

`~/.ai/skills/orchestrate/bin/orchestrate` (backend runtime)

Backend responsibilities:
- task scheduling and dependency checks
- worker lifecycle management
- state persistence in `.ai/team/`
- review/approval APIs (`orchestrate api ...`)

## Build

```bash
cd ~/.ai/skills/orchestrate
go build -o bin/orchestrate ./cmd/main.go
```

## Runtime Commands (called by workflow skill)

```bash
orchestrate start --workflow <template>
orchestrate status
orchestrate logs
orchestrate stop
orchestrate approve <task-id>
orchestrate templates
```

## Worker API Commands

```bash
orchestrate api create-task --input '{...}'
orchestrate api update-task --input '{...}'
orchestrate api claim-task --input '{...}'
orchestrate api start-task --input '{...}'
orchestrate api complete-task --input '{...}'
orchestrate api fail-task --input '{...}'
orchestrate api request-review --input '{...}'
orchestrate api check-review --input '{...}'
```

## Notes

- Binary is intentionally not tracked in git (`bin/orchestrate`).
- Templates are loaded from project/user/bundled template paths.
- Team runtime state is stored under `.ai/team/`.

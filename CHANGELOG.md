# Changelog

Functional changes per commit. Updated as part of pre-commit docs check.

Format: one entry per meaningful change, newest first.

## Unreleased

### Changed
- docs: rewrote root `README.md` — removed non-existent `ag` CLI and `context_management` tool, fixed config/env vars/tools/skills sections, added complete RPC event types
- docs: created README for `pkg/agentconfig`, `pkg/middlewares`, `pkg/testutil` (previously missing)
- docs: strengthened `CLAUDE.md` Documentation Maintenance section — added pre-commit checklist and documentation surface table
- docs: updated `docs/README.md` — added root README and `pkg/*/README.md` to Live Docs index
- docs: verified and fixed all `pkg/*/README.md` against actual code (types, functions, file references)
- docs: restructured documentation — live docs, ADRs, and archive separated; added `docs/README.md` as entry point
- docs: rewrote `context-management.md` to match actual LLMDecide architecture (removed obsolete ContextManager/truncate/update tools)
- docs: rewrote `session-format.md` — added `snapshotRef` + checkpoint layout, removed non-existent `compact_event` entry type
- docs: updated `pkg/compact/README.md` and `pkg/context/README.md` to remove stale ContextManager references
- docs: updated `pkg/agent/README.md` — replaced `compaction_controller.go` with actual files, added missing entries
- docs: updated `pkg/session/README.md` — removed `compact_event` type, fixed session header fields, fixed compaction entry format
- docs: updated `architecture.md` — fixed package tree, removed `skills/ag` and `context_mgmt` references, updated context management flow
- docs: updated `rpc-protocol.md` — removed `skills/ag` reference
- docs: updated `test-strategy.md` — removed `skills/ag` test files, fixed stale test file reference
- docs: updated `CLAUDE.md` — fixed stale code paths, added `agentconfig`/`middlewares` packages, added Documentation Maintenance section
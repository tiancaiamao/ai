# Archive

Historical design proposals. Not maintained — read for context only.

These documents capture the design thinking behind features that were implemented, superseded, or abandoned. See [CHANGELOG.md](../../CHANGELOG.md) for the full architectural history.

## Documents

| Document | Era | Fate |
|----------|-----|------|
| `ai-agent-control.md` | PGE era (2026-05) | ✅ Implemented — steer/send/watch became native `ai` CLI commands |
| `cache-friendly-message-architecture.md` | Context v3 (2026-05) | ✅ Implemented — `buildCacheFriendlyLLMContext` in `pkg/compact/` |
| `agent-harness-evolution.md` | Harness separation (2026-05) | ✅ Implemented — hook system + `pkg/agentconfig/` + `pkg/middlewares/` |
| `agent-harness-evolve-v1.md` | Harness separation (2026-05) | ✅ Implemented — same as above, earlier draft |
| `agent-harness-evolve-step-by-step.md` | Harness separation (2026-05) | ✅ Implemented — incremental steps completed |
| `evolve-directions.md` | Auto-evolution (2026-06) | ✅ Implemented — autonomous prompt-optimization loop |
| `evolve-output-spec.md` | Auto-evolution (2026-06) | 📋 Historical — output format spec, evolve loop no longer active |
| `planner-system-prompt.md` | Auto-evolution (2026-06) | 📋 Historical — planner prompt for evolve loop |
| `agent-debugger-design.md` | Auto-evolution (2026-06) | 🔬 Exploratory — trace-level debugging concepts partially in `pkg/traceevent/` |
| `plan-format-analysis.md` | Plan format (2026-05) | ✅ Implemented — migrated from YAML to Markdown |
| `test-analysis.md` | Test infra (2026-05) | 📋 Historical — see `docs/test-strategy.md` instead |
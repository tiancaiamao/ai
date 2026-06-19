# Archive

Design proposals and historical documents. **Not maintained.**

These documents were created during the design phase of various features.
Some have been fully implemented; others were exploratory or superseded.

## Implementation Status

| Document | Status | Notes |
|----------|--------|-------|
| `ai-agent-control.md` | ✅ Implemented | Steer/abort commands implemented in `pkg/run/` and RPC handlers |
| `cache-friendly-message-architecture.md` | ✅ Implemented | `buildCacheFriendlyLLMContext` in `pkg/compact/compact.go` |
| `agent-harness-evolve-v1.md` | ✅ Implemented | Core agent loop refactored into structured state machine |
| `agent-harness-evolve-step-by-step.md` | ✅ Implemented | Incremental evolution steps completed |
| `agent-debugger-design.md` | 🔬 Exploratory | Debugging concepts partially in trace event system |
| `design-skill-progressive-disclosure.md` | ✅ Implemented | Progressive disclosure in `pkg/skill/` skill loader |
| `design.md` | 📋 Superseded | Original project design, see live docs instead |
| `evolve-directions.md` | 📋 Historical | Evolution directions for reference only |
| `evolve-output-spec.md` | 📋 Historical | Output spec exploration |
| `plan-format-analysis.md` | 📋 Historical | Plan format analysis |
| `planner-system-prompt.md` | 📋 Historical | Planner prompt design |
| `tasks.yml` | 📋 Historical | Task definitions from early development |
| `test-analysis.md` | 📋 Historical | Test strategy analysis, see `test-strategy.md` instead |
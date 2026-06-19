# Archive

Design proposals and historical documents. **Not maintained.**

These documents were created during the design phase of various features.
Some have been fully implemented; others were exploratory or superseded.

## Implementation Status

| Document | Status | Notes |
|----------|--------|-------|
| `ai-agent-control.md` | ✅ Implemented | Steer/abort commands in `pkg/run/` and RPC handlers |
| `cache-friendly-message-architecture.md` | ✅ Implemented | `buildCacheFriendlyLLMContext` in `pkg/compact/compact.go` |
| `agent-harness-evolve-v1.md` | ✅ Implemented | Agent loop refactored into structured state machine |
| `agent-harness-evolve-step-by-step.md` | ✅ Implemented | Incremental evolution steps completed |
| `agent-harness-evolution.md` | 📋 Superseded | Original harness evolution design, see live docs instead |
| `agent-debugger-design.md` | 🔬 Exploratory | Debugging concepts partially in trace event system |
| `evolve-directions.md` | 📋 Historical | Evolution directions for reference only |
| `evolve-output-spec.md` | 📋 Historical | Output spec exploration |
| `plan-format-analysis.md` | 📋 Historical | Plan format analysis |
| `planner-system-prompt.md` | 📋 Historical | Planner prompt design |
| `test-analysis.md` | 📋 Historical | Test strategy analysis, see `test-strategy.md` instead |
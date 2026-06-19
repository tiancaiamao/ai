# Archive

Historical documents that are no longer active but kept for reference.

These differ from live docs: they capture a snapshot in time or a decision that
has been made and finalized. They will not be updated.

## Documents

| Document | Why Archived |
|----------|-------------|
| `cache-friendly-message-architecture.md` | Design proposed CacheMode dual-mode (cache-first/context-first) + runtime_state persistence + MessageMutationPolicy. CacheMode was removed in `b28a112` (#305) when context management system was replaced by LLMDecide. The `buildCacheFriendlyLLMContext` that exists today is a different, simpler concept — just prefix alignment for compaction LLM calls. |
| `agent-debugger-design.md` | Design for benchmark debugger; `benchmark/` directory was moved out of this repo. Concepts partially live on in `scripts/evolve_loop.sh` and `agent/benchmarks/`. |
| `plan-format-analysis.md` | Decision record: YAML → Markdown plan format migration. Decision is final, no longer needs active maintenance. |
| `test-analysis.md` | One-time test coverage snapshot (branch `test-infra`). Numbers are long outdated. See `docs/test-strategy.md` for current test guidance. |
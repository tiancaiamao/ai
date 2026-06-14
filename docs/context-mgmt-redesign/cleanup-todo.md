# Cleanup TODO: Legacy Context Management Code

> Status: Post-delta-compaction cleanup plan
> Related: PR #292, design doc `docs/context-mgmt-redesign/final-v3.md`

This document tracks legacy code that was superseded by the delta compaction redesign.
Each item lists the file, what it does, why it's deprecated, and the cleanup action.

---

## 1. ContextManager (independent LLM-driven context management)

**Status: Deprecated — replaced by inline delta compaction (D3)**

ContextManager makes an independent LLM call with dedicated tools (truncate, update, compact, no_action) to manage context. Delta compaction replaces this with inline compaction via the main agent, which is 42x cheaper.

| File | Lines | Action |
|------|-------|--------|
| `pkg/compact/context_management.go` | 899 | **Delete entirely** |
| `pkg/compact/context_management_test.go` | 555 | **Delete entirely** |
| `pkg/compact/compact_coverage_test.go` | ~600 (ContextManager tests) | **Remove ContextManager test sections** (keep Compactor tests) |

### Wiring to remove:
- `cmd/ai/rpc_setup.go:295-336` — `createCompactors()` returns `ContextManager`; remove `ContextManager` creation, simplify signature
- `cmd/ai/rpc_setup.go:154-156` — `ctxManager.SetSkipCondition(...)` call; remove entirely
- `cmd/ai/rpc_setup.go:92` — `createCompactors()` call; update to 2-return
- `cmd/ai/rpc_app.go:66` — `ctxManager *compact.ContextManager` field; remove
- `cmd/ai/rpc_handlers.go:68` — `app.ctxManager` in `WithCompactors()` slice; remove from compactor chain

---

## 2. Context Management Tools (truncate_messages, update_llm_context, no_action, compact)

**Status: Deprecated — delta compaction replaces all of these (D12, D15)**

| File | Lines | Action |
|------|-------|--------|
| `pkg/tools/context_mgmt/truncate_messages.go` | 140 | **Delete** (D12: no standalone truncate) |
| `pkg/tools/context_mgmt/update_llm_context.go` | 77 | **Delete** (delta summary replaces LLMContext) |
| `pkg/tools/context_mgmt/no_action.go` | 53 | **Delete** (no longer needed) |
| `pkg/tools/context_mgmt/registry.go` | 26 | **Delete** (whole package becomes empty) |
| `pkg/tools/context_mgmt/tools_test.go` | 239 | **Delete** |

---

## 3. CompactTool (tool exposed to ContextManager)

**Status: Deprecated — only used by ContextManager which is being removed**

| File | Lines | Action |
|------|-------|--------|
| `pkg/compact/compact_tool.go` | 154 | **Delete** |
| `pkg/compact/compact_tools.go` | 309 | **Review**: check if any functions are used outside ContextManager; delete unused |
| `pkg/compact/compact_tool_pairing_test.go` | 575 | **Delete** |

---

## 4. Context Management System Prompt

**Status: Deprecated — was the system prompt for ContextManager's independent LLM call**

| File | Lines | Action |
|------|-------|--------|
| `pkg/prompt/context_management.md` | 173 | **Delete** |
| `pkg/prompt/builder.go:26` | — | **Remove `//go:embed "context_management.md"`** and related variable |

---

## 5. LLMContext Active Write Path

**Status: Retained for backward compat, but active write paths should be removed**

The `LLMContext` field stays in `AgentContext`/`AgentState`/`ContextSnapshot` for loading old sessions. But the injection and update logic is dead code in the new design.

| Location | Action |
|----------|--------|
| `pkg/agent/llm_stream.go` — `<llm_context>` block injection (`buildRuntimeUserAppendix`) | **Remove LLMContext injection**; delta summaries are now the task state source |
| `pkg/agent/llm_stream.go` — `buildRuntimeUserAppendix` function | **Simplify**: only emit runtime_state, no llm_context section |
| `pkg/context/llm_context.go` (LLMContext file manager, 267 lines) | **Review**: may still be needed for heavyweight compactor's summary storage; if not, delete |
| `pkg/agent/llm_stream.go` — `<llm_context>` test assertions | **Update tests** |

---

## 6. CompactEventDetail / CompactAction (context management events)

**Status: Deprecated — was used by ContextManager to record truncate/update operations**

| Location | Action |
|----------|--------|
| `pkg/context/context.go:42-55` — `CompactAction` type, `CompactActionTruncate`, `CompactActionUpdateLLMContext` | **Remove** these types |
| `pkg/context/context.go:29-31` — `OnCompactEvent` callback | **Remove** |
| `pkg/context/journal.go` — `TruncateEvent`, `EntryTypeTruncate` | **Keep** for old session compat, but mark deprecated |
| `pkg/context/reconstruction.go:103` — `ApplyTruncateToSnapshot` | **Keep** for old session compat |
| `cmd/ai/rpc_helpers.go:171` — `AppendCompactEvent` usage | **Remove** |
| `pkg/session/session.go:316` — `EntryTypeCompactEvent` in `AppendCompactEvent` | **Remove** (or keep for compat) |

---

## 7. Documentation Updates

| File | Action |
|------|--------|
| `pkg/compact/README.md` | Remove ContextManager section |
| `pkg/context/README.md` | Update to reflect delta summary replacing LLMContext |
| `docs/context-management.md` | Rewrite to describe delta compaction architecture |
| `pkg/prompt/README.md` | Remove context_management.md reference |

---

## 8. Test Cleanup

Tests that reference removed code:

| File | Action |
|------|--------|
| `pkg/compact/compact_coverage_test.go` | Remove ContextManager, CompactTool test sections |
| `pkg/agent/loop_stream_integration_test.go` | Update LLMContext injection tests |
| `pkg/agent/loop_recovery_test.go` | Update `update_llm_context` references |
| `pkg/context/reconstruction_counters_test.go` | Update `update_llm_context` tracking tests |

---

## Cleanup Order (suggested)

1. **Remove ContextManager + tools** (items 1-3) — largest deletion, must verify build
2. **Remove prompt** (item 4) — trivial after #1
3. **Remove LLMContext injection** (item 5) — needs careful testing
4. **Remove CompactEventDetail** (item 6) — check for remaining references
5. **Update docs** (item 7-8)

## Estimated Impact

- **~3,300 lines deleted** (context_management.go + tests + tools + prompt + compact_tool)
- **~500 lines modified** (wiring removal, test updates, doc rewrites)
- Net: significant simplification of the context management codebase
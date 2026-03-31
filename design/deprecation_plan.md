# Deprecation Plan: Clean Slate for New Architecture

## Directories to Deprecate

| Directory | Reason | New Name |
|-----------|--------|----------|
| `pkg/context/` | Old context management (ContextMgmtState, etc.) | `pkg/context_deprecated/` |
| `pkg/compact/` | Old compaction logic (replaced by event sourcing) | `pkg/compact_deprecated/` |
| `pkg/truncate/` | Old truncate logic (replaced by journal events) | `pkg/truncate_deprecated/` |

## Files to Deprecate

| File | Reason | New Name |
|------|--------|----------|
| `pkg/tools/context_management.go` | Old context management tool | `context_management_deprecated.go` |
| `pkg/tools/context_management_test.go` | Old tests | `context_management_deprecated_test.go` |

## Directories to Keep (Will be reused)

| Directory | What will be reused |
|-----------|-------------------|
| `pkg/agent/` | Basic loop structure, but will rewrite context mgmt parts |
| `pkg/config/` | Configuration loading |
| `pkg/llm/` | LLM protocol |
| `pkg/logger/` | Logging utilities |
| `pkg/modelselect/` | Model selection |
| `pkg/rpc/` | RPC interface |
| `pkg/session/` | Partially - basic I/O, but will restructure |
| `pkg/skill/` | Skill loading and execution |
| `pkg/tools/` | Tool framework (remove context_management.go) |
| `pkg/traceevent/` | Observability (unchanged) |

## Files in pkg/context to inspect

Some files in `pkg/context/` may have reusable parts:
- `message.go` - Message structures may be reusable
- `llm_context.go` - LLMContext file I/O may be reusable
- `tool_tag.go` - Tool tag utilities

## Execution Order

1. Rename directories (context, compact, truncate)
2. Rename files in pkg/tools/
3. Check for import statements that need updating
4. Run tests to verify nothing breaks
5. Commit with message: "chore: deprecate old context management code"

## After Commit

Start implementing new architecture in parallel:
- Keep deprecated code for reference
- New code goes into new files
- Eventually remove deprecated code after validation
